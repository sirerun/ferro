package wake

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

const maxWakeBodyBytes = 1024

type ServiceUpdater interface {
	UpdateService(context.Context, *ecs.UpdateServiceInput, ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error)
}

type Handler func(context.Context, events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, error)

func NewHandler(token, serviceARN, publicHost string, updater ServiceUpdater) (Handler, error) {
	if len(token) < 32 {
		return nil, fmt.Errorf("wake bearer token must contain at least 32 bytes")
	}
	if !strings.HasPrefix(serviceARN, "arn:aws:ecs:") || strings.ContainsAny(serviceARN, "*?\r\n") || publicHost == "" || strings.ContainsAny(publicHost, "/:@*?\r\n ") || updater == nil {
		return nil, fmt.Errorf("exact ECS service ARN, host, and updater are required")
	}
	return func(ctx context.Context, request events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, error) {
		host, hostOK := singleHeader(request.Headers, "host")
		if request.HTTPMethod != "POST" || request.Path != "/bridge/wake" || !hostOK || !strings.EqualFold(host, publicHost) {
			return response(404, `{"error":"not found"}`), nil
		}
		body := request.Body
		if request.IsBase64Encoded {
			decoded, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				return response(400, `{"error":"invalid request"}`), nil
			}
			body = string(decoded)
		}
		if len(body) > maxWakeBodyBytes {
			return response(413, `{"error":"request too large"}`), nil
		}
		if body != "" {
			return response(400, `{"error":"request body must be empty"}`), nil
		}
		authorization, authorizationOK := singleHeader(request.Headers, "authorization")
		provided, ok := strings.CutPrefix(authorization, "Bearer ")
		if !authorizationOK || !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			return response(401, `{"error":"unauthorized"}`), nil
		}
		_, err := updater.UpdateService(ctx, &ecs.UpdateServiceInput{Service: aws.String(serviceARN), DesiredCount: aws.Int32(1)})
		if err != nil {
			return response(502, `{"error":"wake request failed"}`), nil
		}
		return response(202, `{"status":"requested"}`), nil
	}, nil
}

func singleHeader(headers map[string]string, name string) (string, bool) {
	var value string
	found := false
	for key, headerValue := range headers {
		if strings.EqualFold(key, name) {
			if found {
				return "", false
			}
			value, found = headerValue, true
		}
	}
	return value, found
}

func response(status int, body string) events.ALBTargetGroupResponse {
	return events.ALBTargetGroupResponse{
		StatusCode:        status,
		StatusDescription: fmt.Sprintf("%d %s", status, statusText(status)),
		IsBase64Encoded:   false,
		Headers:           map[string]string{"content-type": "application/json", "cache-control": "no-store"},
		Body:              body,
	}
}

func statusText(status int) string {
	switch status {
	case 202:
		return "Accepted"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 404:
		return "Not Found"
	case 413:
		return "Payload Too Large"
	case 502:
		return "Bad Gateway"
	default:
		return "Error"
	}
}
