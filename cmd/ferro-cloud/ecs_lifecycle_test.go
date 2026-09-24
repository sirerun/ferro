package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestProtectionEndpointRejectsRemoteAndAmbiguousTargets(t *testing.T) {
	for _, raw := range []string{"https://169.254.170.2", "http://example.com", "http://169.254.170.3", "http://user@127.0.0.1", "http://127.0.0.1/other", "http://127.0.0.1?q=1", "http://127.0.0.1#x"} {
		if _, err := protectionEndpoint(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"http://169.254.170.2", "http://127.0.0.1:1234"} {
		if _, err := protectionEndpoint(raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProtectionRequiresAcknowledgedUnexpiredState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		status  int
		body    string
		ok      bool
	}{
		{"protected", true, 200, `{"protection":{"ProtectionEnabled":true,"ExpirationDate":"` + time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339) + `","TaskArn":"task"}}`, true},
		{"released", false, 200, `{"protection":{"ProtectionEnabled":false,"ExpirationDate":null,"TaskArn":"task"}}`, true},
		{"missing state", false, 200, `{"protection":{"TaskArn":"task"}}`, false},
		{"wrong state", true, 200, `{"protection":{"ProtectionEnabled":false,"TaskArn":"task"}}`, false},
		{"expired", true, 200, `{"protection":{"ProtectionEnabled":true,"ExpirationDate":"2020-01-01T00:00:00Z","TaskArn":"task"}}`, false},
		{"error envelope", true, 200, `{"error":{"Code":"AccessDenied"}}`, false},
		{"failure envelope", true, 200, `{"failure":{"Reason":"TASK_NOT_VALID"}}`, false},
		{"non success", true, 500, `{}`, false},
		{"invalid JSON", true, 200, `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PUT" || r.URL.Path != "/task-protection/v1/state" {
					t.Errorf("wrong request %s %s", r.Method, r.URL.Path)
				}
				var input struct {
					Enabled bool `json:"ProtectionEnabled"`
					Minutes int  `json:"ExpiresInMinutes"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input.Enabled != tc.enabled || (tc.enabled && input.Minutes != 5) {
					t.Errorf("wrong protection request %+v", input)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			endpoint, err := protectionEndpoint(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			c := &ecsIdleController{http: server.Client(), endpoint: endpoint}
			if err := c.Protect(context.Background(), tc.enabled); (err == nil) != tc.ok {
				t.Fatalf("ok=%v error=%v", tc.ok, err)
			}
		})
	}
}

type serviceUpdateFixture struct {
	input  *ecs.UpdateServiceInput
	result *ecs.UpdateServiceOutput
	err    error
}

func (f *serviceUpdateFixture) UpdateService(_ context.Context, in *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	f.input = in
	return f.result, f.err
}
func TestSleepCanOnlyStopConfiguredService(t *testing.T) {
	service := "arn:aws:ecs:us-west-2:123456789012:service/example/example"
	fake := &serviceUpdateFixture{result: &ecs.UpdateServiceOutput{Service: &types.Service{ServiceArn: aws.String(service), DesiredCount: 0}}}
	c := &ecsIdleController{client: fake, cluster: "example", service: service}
	if err := c.Sleep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(fake.input.Service) != service || aws.ToString(fake.input.Cluster) != "example" || fake.input.DesiredCount == nil || *fake.input.DesiredCount != 0 || fake.input.TaskDefinition != nil || fake.input.ForceNewDeployment {
		t.Fatalf("unexpected update %+v", fake.input)
	}
	fake.err = errors.New("control plane unavailable")
	if err := c.Sleep(context.Background()); err == nil {
		t.Fatal("ambiguous update reported successful")
	}
	fake.err = nil
	fake.result.Service.DesiredCount = 1
	if err := c.Sleep(context.Background()); err == nil {
		t.Fatal("wrong desired count accepted")
	}
}
