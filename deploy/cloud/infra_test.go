package main

import (
	"encoding/json"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func validInputs() inputs {
	return inputs{
		stage: "activate", region: "us-west-2", accountID: "123456789012", publicDomain: "ferro.example.test",
		albDNSName: "shared-alb.example.test", imageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		vpcID: "vpc-0123456789abcdef0", albSecurityGroupID: "sg-0123456789abcdef0",
		albListenerARN:  "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/shared/abc/def",
		publicSubnetIDs: []string{"subnet-0123456789abcdef0", "subnet-1123456789abcdef0"},
		proxyCIDRs:      []string{"10.0.0.0/16"}, validationRecordFQDNs: []string{"_validation.ferro.example.test"},
		wakeZipPath: "wake.zip", wakeRulePriority: 43000, appRulePriority: 43001,
	}
}

func TestValidateInputsFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*inputs)
	}{
		{"mutable image tag", func(in *inputs) { in.imageDigest = "latest" }},
		{"public proxy range", func(in *inputs) { in.proxyCIDRs = []string{"0.0.0.0/0"} }},
		{"noncanonical proxy range", func(in *inputs) { in.proxyCIDRs = []string{"10.0.0.1/16"} }},
		{"duplicate subnets", func(in *inputs) { in.publicSubnetIDs[1] = in.publicSubnetIDs[0] }},
		{"listener wrong account", func(in *inputs) {
			in.albListenerARN = "arn:aws:elasticloadbalancing:us-west-2:999999999999:listener/app/shared/abc/def"
		}},
		{"duplicate priorities", func(in *inputs) { in.appRulePriority = in.wakeRulePriority }},
		{"app rule shadows wake path", func(in *inputs) { in.wakeRulePriority, in.appRulePriority = 43001, 43000 }},
		{"uppercase public host", func(in *inputs) { in.publicDomain = "Ferro.example.test" }},
		{"missing ALB DNS name", func(in *inputs) { in.albDNSName = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInputs()
			tt.edit(&in)
			if err := validateInputs(in); err == nil {
				t.Fatal("validateInputs accepted invalid configuration")
			}
		})
	}
}

func TestPolicyScopeAndTaskDefinition(t *testing.T) {
	policy, err := policyJSON(policyStatement{Effect: "Allow", Action: []string{"ecr:GetAuthorizationToken"}, Resource: []string{"*"}, Condition: map[string]map[string]string{"StringEquals": {"aws:RequestedRegion": "us-west-2"}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Statement []policyStatement
	}
	if err := json.Unmarshal([]byte(policy), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Statement) != 1 || decoded.Statement[0].Resource[0] != "*" || decoded.Statement[0].Condition["StringEquals"]["aws:RequestedRegion"] != "us-west-2" {
		t.Fatalf("ECR authorization exception lost its region boundary: %#v", decoded.Statement)
	}

	defs, err := taskContainerDefinitions([]any{"repo.example.test/ferro@sha256:digest", "mcp-secret-arn", "bridge-secret-arn", "/ecs/ferro"}, validInputs())
	if err != nil {
		t.Fatal(err)
	}
	var containers []struct {
		Image                  string              `json:"image"`
		User                   string              `json:"user"`
		ReadonlyRootFilesystem bool                `json:"readonlyRootFilesystem"`
		Environment            []map[string]string `json:"environment"`
		Secrets                []map[string]string `json:"secrets"`
		MountPoints            []map[string]any    `json:"mountPoints"`
	}
	if err := json.Unmarshal([]byte(defs), &containers); err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].User != "65532:65532" || !containers[0].ReadonlyRootFilesystem || containers[0].Image != "repo.example.test/ferro@sha256:digest" {
		t.Fatalf("unsafe container defaults: %#v", containers)
	}
	if len(containers[0].Secrets) != 2 || containers[0].Secrets[0]["valueFrom"] != "mcp-secret-arn" || len(containers[0].MountPoints) != 2 {
		t.Fatalf("container secret injection or writable mounts missing: %#v", containers[0])
	}
}

func TestAWSConditionKeysAndTrustBoundaries(t *testing.T) {
	efs, err := efsClientPolicy("arn:aws:elasticfilesystem:us-west-2:123456789012:file-system/fs-123", "arn:aws:elasticfilesystem:us-west-2:123456789012:access-point/fsap-123")
	if err != nil {
		t.Fatal(err)
	}
	var efsPolicy struct {
		Statement []policyStatement
	}
	if err := json.Unmarshal([]byte(efs), &efsPolicy); err != nil {
		t.Fatal(err)
	}
	condition := efsPolicy.Statement[0].Condition
	if condition["StringEquals"]["elasticfilesystem:AccessPointArn"] == "" {
		t.Fatalf("EFS policy is not scoped to an access point: %#v", condition)
	}
	if _, exists := condition["StringEquals"]["elasticfilesystem:EncryptedInTransit"]; exists {
		t.Fatal("EFS policy contains unsupported EncryptedInTransit condition key")
	}

	ecsTrust, err := trustPolicy("ecs-tasks.amazonaws.com", "us-west-2", "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	var trust struct {
		Statement []struct {
			Condition map[string]map[string]string
		}
	}
	if err := json.Unmarshal([]byte(ecsTrust), &trust); err != nil {
		t.Fatal(err)
	}
	if trust.Statement[0].Condition["ArnLike"]["aws:SourceArn"] != "arn:aws:ecs:us-west-2:123456789012:*" {
		t.Fatalf("ECS trust has unsupported or overnarrow SourceArn: %#v", trust.Statement[0].Condition)
	}
	lambdaTrust, err := trustPolicy("lambda.amazonaws.com", "us-west-2", "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	trust = struct {
		Statement []struct {
			Condition map[string]map[string]string
		}
	}{}
	if err := json.Unmarshal([]byte(lambdaTrust), &trust); err != nil {
		t.Fatal(err)
	}
	if _, exists := trust.Statement[0].Condition["ArnLike"]; exists {
		t.Fatalf("Lambda execution-role trust must not require SourceArn: %#v", trust.Statement[0].Condition)
	}
}

func TestEFSResourcePolicyScopesTaskRoleAccessPointAndTLS(t *testing.T) {
	policyJSON, err := efsFileSystemPolicy("fs-arn", "ap-arn", "task-role-arn")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Statement []map[string]any
	}
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		t.Fatal(err)
	}
	bySid := make(map[string]map[string]any)
	for _, statement := range policy.Statement {
		bySid[statement["Sid"].(string)] = statement
	}
	for _, sid := range []string{"AllowTaskRoleViaAccessPointTLS"} {
		if bySid[sid] == nil {
			t.Fatalf("missing filesystem policy statement %q: %#v", sid, policy.Statement)
		}
	}
	allow := bySid["AllowTaskRoleViaAccessPointTLS"]
	if allow["Principal"].(map[string]any)["AWS"] != "task-role-arn" || allow["Condition"].(map[string]any)["StringEquals"].(map[string]any)["elasticfilesystem:AccessPointArn"] != "ap-arn" || allow["Condition"].(map[string]any)["Bool"].(map[string]any)["aws:SecureTransport"] != "true" {
		t.Fatalf("filesystem policy allow is not scoped to role, access point, and TLS: %#v", allow)
	}
	if len(policy.Statement) != 1 {
		t.Fatalf("filesystem policy has unsupported or unexpected statements: %#v", policy.Statement)
	}
}

type bootstrapMocks struct{ resources []string }

func (m *bootstrapMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *bootstrapMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.resources = append(m.resources, args.TypeToken)
	return args.Name + "-id", args.Inputs, nil
}

func TestBootstrapRegistersOnlyOwnedResources(t *testing.T) {
	mocks := &bootstrapMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return deployHostedPilot(ctx, inputs{stage: "bootstrap", region: "us-west-2", accountID: "123456789012", publicDomain: "ferro.example.test", albDNSName: "shared-alb.example.test"})
	}, pulumi.WithMocks("ferro-hosted", "test", mocks))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, token := range mocks.resources {
		seen[token] = true
	}
	if !seen["aws:ecr/repository:Repository"] || !seen["aws:acm/certificate:Certificate"] {
		t.Fatalf("bootstrap omitted expected repository or certificate: %v", mocks.resources)
	}
	if seen["aws:ecs/service:Service"] || seen["aws:lb/listenerRule:ListenerRule"] {
		t.Fatalf("bootstrap unexpectedly registered active resources: %v", mocks.resources)
	}
}
