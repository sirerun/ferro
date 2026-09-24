package wake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

type updateRecorder struct {
	calls       int
	cluster     string
	service     string
	desired     int32
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (r *updateRecorder) UpdateService(ctx context.Context, input *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	r.calls++
	if input.Cluster != nil {
		r.cluster = *input.Cluster
	}
	if input.Service != nil {
		r.service = *input.Service
	}
	if input.DesiredCount != nil {
		r.desired = *input.DesiredCount
	}
	r.deadline, r.hasDeadline = ctx.Deadline()
	return &ecs.UpdateServiceOutput{}, r.err
}

func TestWakeHandlerAuthenticatesAndTargetsOnlyConfiguredService(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const serviceARN = "arn:aws:ecs:us-west-2:123456789012:service/ferro-hosted/ferro-hosted"
	updater := &updateRecorder{}
	handler, err := NewHandler(token, serviceARN, "ferro.example", updater)
	if err != nil {
		t.Fatal(err)
	}
	request := events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Headers: map[string]string{"authorization": "Bearer " + token, "host": "ferro.example"}}
	response, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 202 || response.Body != `{"status":"requested"}` {
		t.Fatalf("response=%+v", response)
	}
	if updater.calls != 1 || updater.cluster != "ferro-hosted" || updater.service != serviceARN || updater.desired != 1 {
		t.Fatalf("update calls=%d cluster=%q service=%q desired=%d", updater.calls, updater.cluster, updater.service, updater.desired)
	}
	if !updater.hasDeadline {
		t.Fatal("UpdateService context has no bounded deadline")
	}
	if remaining := time.Until(updater.deadline); remaining <= 9*time.Second || remaining > 10*time.Second {
		t.Fatalf("UpdateService deadline remaining=%s, want at most 10s and near the full budget", remaining)
	}
}

func TestNewHandlerRejectsMalformedServiceARN(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, arn := range []string{
		"arn:aws:ecs:us-west-2:123456789012:service/ferro-hosted",
		"arn:aws:ecs:us-west-2:123456789012:service//ferro-hosted",
		"arn:aws:ecs:us-west-2:123456789012:service/ferro-hosted/name/extra",
		"arn:aws:ecs:us-west-2:123456789012:service/ferro-hosted/*",
	} {
		if _, err := NewHandler(token, arn, "ferro.example", &updateRecorder{}); err == nil {
			t.Errorf("NewHandler accepted malformed service ARN %q", arn)
		}
	}
}

func TestWakeHandlerRejectsUnauthenticatedOrCallerSelectedTargetsBeforeAWS(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	updater := &updateRecorder{}
	handler, err := NewHandler(token, "arn:aws:ecs:us-west-2:123456789012:service/cluster/service", "ferro.example", updater)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		request events.ALBTargetGroupRequest
		status  int
	}{
		{"wrong method", events.ALBTargetGroupRequest{HTTPMethod: "GET", Path: "/bridge/wake", Headers: map[string]string{"authorization": "Bearer " + token, "host": "ferro.example"}}, 404},
		{"wrong path", events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake/other", Headers: map[string]string{"authorization": "Bearer " + token, "host": "ferro.example"}}, 404},
		{"wrong host", events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Headers: map[string]string{"authorization": "Bearer " + token, "host": "attacker.example"}}, 404},
		{"bad token", events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Headers: map[string]string{"Authorization": "Bearer wrong", "host": "ferro.example"}}, 401},
		{"caller target body", events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Body: `{"service":"arn:aws:ecs:other"}`, Headers: map[string]string{"Authorization": "Bearer " + token, "host": "ferro.example"}}, 400},
		{"oversized", events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Body: strings.Repeat("x", maxWakeBodyBytes+1), Headers: map[string]string{"Authorization": "Bearer " + token, "host": "ferro.example"}}, 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := handler(context.Background(), tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
			}
		})
	}
	if updater.calls != 0 {
		t.Fatalf("unauthorized/malformed requests dispatched %d AWS updates", updater.calls)
	}
}

func TestWakeHandlerReturnsGenericFailureWithoutLeakingAWSDetails(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	updater := &updateRecorder{err: context.DeadlineExceeded}
	handler, err := NewHandler(token, "arn:aws:ecs:us-west-2:123456789012:service/cluster/service", "ferro.example", updater)
	if err != nil {
		t.Fatal(err)
	}
	response, err := handler(context.Background(), events.ALBTargetGroupRequest{HTTPMethod: "POST", Path: "/bridge/wake", Headers: map[string]string{"Authorization": "Bearer " + token, "host": "ferro.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 502 || strings.Contains(response.Body, "DeadlineExceeded") {
		t.Fatalf("sensitive or misleading response: %+v", response)
	}
}
