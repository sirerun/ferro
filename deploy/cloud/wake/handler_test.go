package wake

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

type updateRecorder struct {
	calls   int
	service string
	desired int32
	err     error
}

func (r *updateRecorder) UpdateService(_ context.Context, input *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	r.calls++
	if input.Service != nil {
		r.service = *input.Service
	}
	if input.DesiredCount != nil {
		r.desired = *input.DesiredCount
	}
	return &ecs.UpdateServiceOutput{}, r.err
}

func TestWakeHandlerAuthenticatesAndTargetsOnlyConfiguredService(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const serviceARN = "arn:aws:ecs:us-west-2:123456789012:service/cluster/service"
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
	if updater.calls != 1 || updater.service != serviceARN || updater.desired != 1 {
		t.Fatalf("update calls=%d service=%q desired=%d", updater.calls, updater.service, updater.desired)
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
