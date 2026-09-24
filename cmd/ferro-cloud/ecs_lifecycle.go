package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

type ecsServiceUpdater interface {
	UpdateService(context.Context, *ecs.UpdateServiceInput, ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error)
}

type ecsIdleController struct {
	client   ecsServiceUpdater
	http     *http.Client
	endpoint string
	cluster  string
	service  string
}

var ecsServiceARN = regexp.MustCompile(`^arn:aws:ecs:([a-z0-9-]+):[0-9]{12}:service/([A-Za-z0-9_-]+)/([A-Za-z0-9_-]+)$`)

func newECSIdleController(ctx context.Context, serviceARN, region, agentURI string) (*ecsIdleController, error) {
	parts := ecsServiceARN.FindStringSubmatch(serviceARN)
	if len(parts) != 4 || region == "" || parts[1] != region {
		return nil, errors.New("ECS service must be one exact regional service ARN")
	}
	endpoint, err := protectionEndpoint(agentURI)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithRetryMaxAttempts(1))
	if err != nil {
		return nil, fmt.Errorf("load ECS task credentials: %w", err)
	}
	return &ecsIdleController{client: ecs.NewFromConfig(cfg), http: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: endpoint, cluster: parts[2], service: serviceARN}, nil
}

func protectionEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("ECS agent URI must be a local HTTP origin")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || ip.Zone() != "" || (!ip.IsLoopback() && ip.String() != "169.254.170.2") {
		return "", errors.New("ECS agent URI must identify the local ECS agent")
	}
	return strings.TrimSuffix(raw, "/") + "/task-protection/v1/state", nil
}

// Protect verifies the agent's acknowledgement before readiness changes. The
// five-minute protection is renewed by the lifecycle every thirty seconds.
func (c *ecsIdleController) Protect(ctx context.Context, enabled bool) error {
	payload := struct {
		Enabled bool `json:"ProtectionEnabled"`
		Minutes int  `json:"ExpiresInMinutes,omitempty"`
	}{Enabled: enabled}
	if enabled {
		payload.Minutes = 5
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode task protection: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create task protection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request task protection: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("task protection returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Protection *struct {
			Enabled    *bool     `json:"ProtectionEnabled"`
			Expiration time.Time `json:"ExpirationDate"`
			TaskARN    string    `json:"TaskArn"`
		} `json:"protection"`
		Failure json.RawMessage `json:"failure"`
		Error   json.RawMessage `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil {
		return fmt.Errorf("read task protection: %w", err)
	}
	if len(data) > 16384 || json.Unmarshal(data, &result) != nil {
		return errors.New("invalid task protection response")
	}
	if result.Protection == nil || len(result.Failure) > 0 || len(result.Error) > 0 || (result.Protection.Enabled == nil || *result.Protection.Enabled != enabled) || result.Protection.TaskARN == "" {
		return errors.New("task protection was not acknowledged")
	}
	if enabled && !result.Protection.Expiration.After(time.Now().Add(time.Minute)) {
		return errors.New("task protection expires too soon")
	}
	return nil
}

// Sleep is intentionally incapable of selecting a caller-supplied target or
// changing task definitions. IAM additionally restricts it to this service.
func (c *ecsIdleController) Sleep(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := c.client.UpdateService(ctx, &ecs.UpdateServiceInput{Cluster: aws.String(c.cluster), Service: aws.String(c.service), DesiredCount: aws.Int32(0)})
	if err != nil {
		return fmt.Errorf("request idle ECS service stop: %w", err)
	}
	if result == nil || result.Service == nil || aws.ToString(result.Service.ServiceArn) != c.service || result.Service.DesiredCount != 0 {
		return errors.New("idle ECS service stop was not acknowledged")
	}
	return nil
}
