package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/ajent-social/pulumi/aws/deploymentidentity"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecr"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/efs"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/secretsmanager"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	stackPrefix       = "ferro-hosted"
	serviceName       = "ferro-hosted"
	clusterName       = "ferro-hosted"
	containerName     = "ferro-cloud"
	listenerPort      = 8080
	privateHomePath   = "/var/lib/ferro-cloud"
	serviceLogGroup   = "/ecs/ferro-hosted"
	wakeLogGroup      = "/aws/lambda/ferro-hosted-wake"
	bridgeSecretName  = "ferro-hosted/bridge-token"
	mcpSecretName     = "ferro-hosted/mcp-token"
	wakeFunctionName  = "ferro-hosted-wake"
	identityComponent = "ferro-hosted-deployment"
)

var (
	accountIDPattern   = regexp.MustCompile(`^[0-9]{12}$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	regionPattern      = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
)

type inputs struct {
	stage, region, accountID, publicDomain             string
	imageDigest, vpcID, albSecurityGroupID             string
	albListenerARN, albDNSName, wakeZipPath            string
	publicSubnetIDs, proxyCIDRs, validationRecordFQDNs []string
	wakeRulePriority, appRulePriority                  int
	createDeploymentIdentity                           bool
	identity                                           deploymentIdentityInputs
}

type deploymentIdentityInputs struct {
	roleNamePrefix, providerARN, owner, repository      string
	ownerID, repositoryID, previewRef, applyEnvironment string
	previewPolicyJSON, applyPolicyJSON                  string
}

func loadInputs(ctx *pulumi.Context, cfg *config.Config) (inputs, error) {
	var in inputs
	in.stage = cfg.Require("stage")
	in.region = cfg.Require("region")
	if configuredRegion := config.New(ctx, "aws").Get("region"); configuredRegion == "" || configuredRegion != in.region {
		return in, errors.New("hosted:region and aws:region must both be set to the same explicit region")
	}
	in.accountID = cfg.Require("accountId")
	in.publicDomain = cfg.Require("publicDomain")
	in.albDNSName = cfg.Require("albDnsName")
	in.createDeploymentIdentity = cfg.GetBool("createDeploymentIdentity")
	if cfg.Get("imageDigest") != "" {
		in.imageDigest = cfg.Require("imageDigest")
	}
	if cfg.Get("wakeZipPath") != "" {
		in.wakeZipPath = cfg.Require("wakeZipPath")
	}
	if cfg.Get("vpcId") != "" {
		in.vpcID = cfg.Require("vpcId")
	}
	if cfg.Get("albSecurityGroupId") != "" {
		in.albSecurityGroupID = cfg.Require("albSecurityGroupId")
	}
	if cfg.Get("albListenerArn") != "" {
		in.albListenerARN = cfg.Require("albListenerArn")
	}
	if cfg.Get("publicSubnetIds") != "" {
		if err := cfg.TryObject("publicSubnetIds", &in.publicSubnetIDs); err != nil {
			return in, fmt.Errorf("read publicSubnetIds: %w", err)
		}
	}
	if cfg.Get("trustedProxyCIDRs") != "" {
		if err := cfg.TryObject("trustedProxyCIDRs", &in.proxyCIDRs); err != nil {
			return in, fmt.Errorf("read trustedProxyCIDRs: %w", err)
		}
	}
	if cfg.Get("validationRecordFqdns") != "" {
		if err := cfg.TryObject("validationRecordFqdns", &in.validationRecordFQDNs); err != nil {
			return in, fmt.Errorf("read validationRecordFqdns: %w", err)
		}
	}
	in.wakeRulePriority = cfg.GetInt("wakeRulePriority")
	if in.wakeRulePriority == 0 {
		in.wakeRulePriority = 43000
	}
	in.appRulePriority = cfg.GetInt("appRulePriority")
	if in.appRulePriority == 0 {
		in.appRulePriority = 43001
	}
	if in.createDeploymentIdentity {
		in.identity = deploymentIdentityInputs{
			roleNamePrefix:    cfg.Require("deploymentRoleNamePrefix"),
			providerARN:       cfg.Require("githubOIDCProviderArn"),
			owner:             cfg.Require("githubRepositoryOwner"),
			repository:        cfg.Require("githubRepositoryName"),
			ownerID:           cfg.Get("githubOwnerId"),
			repositoryID:      cfg.Get("githubRepositoryId"),
			previewRef:        cfg.Require("githubPreviewRef"),
			applyEnvironment:  cfg.Require("githubApplyEnvironment"),
			previewPolicyJSON: cfg.Require("previewPolicyJson"),
			applyPolicyJSON:   cfg.Require("applyPolicyJson"),
		}
	}
	if err := validateInputs(in); err != nil {
		return in, err
	}
	return in, nil
}

func validateInputs(in inputs) error {
	if in.stage != "bootstrap" && in.stage != "activate" {
		return errors.New("stage must be bootstrap or activate")
	}
	if !regionPattern.MatchString(in.region) || !accountIDPattern.MatchString(in.accountID) {
		return errors.New("region and 12-digit accountId must be explicit")
	}
	if !validDomain(in.publicDomain) {
		return errors.New("publicDomain must be a canonical lowercase DNS hostname")
	}
	if in.albDNSName == "" || strings.ContainsAny(in.albDNSName, ":/@*? \r\n") || !strings.Contains(in.albDNSName, ".") || strings.ToLower(in.albDNSName) != in.albDNSName {
		return errors.New("albDnsName must be a canonical DNS name without scheme or path")
	}
	if !in.createDeploymentIdentity {
		if in.stage == "bootstrap" {
			return nil
		}
	} else if in.identity.ownerID == "" != (in.identity.repositoryID == "") {
		return errors.New("GitHub owner and repository IDs must be supplied together")
	}
	if in.stage == "bootstrap" {
		return nil
	}
	if !imageDigestPattern.MatchString(in.imageDigest) {
		return errors.New("imageDigest must be a pinned sha256 digest")
	}
	if in.vpcID == "" || in.albSecurityGroupID == "" || in.albListenerARN == "" || in.albDNSName == "" {
		return errors.New("activate stage requires the existing VPC, ALB security group, HTTPS listener ARN, and ALB DNS name")
	}
	if len(in.publicSubnetIDs) < 2 {
		return errors.New("activate stage requires at least two supplied public subnet IDs")
	}
	if len(in.validationRecordFQDNs) == 0 {
		return errors.New("activate stage requires ACM DNS validation record FQDNs after external DNS validation")
	}
	if in.wakeZipPath == "" {
		return errors.New("activate stage requires a built Go custom-runtime Lambda zip path")
	}
	if in.wakeRulePriority < 1 || in.wakeRulePriority > 50000 || in.appRulePriority < 1 || in.appRulePriority > 50000 || in.wakeRulePriority >= in.appRulePriority {
		return errors.New("wakeRulePriority must be lower than appRulePriority and both must be between 1 and 50000")
	}
	if len(in.proxyCIDRs) == 0 {
		return errors.New("activate stage requires explicit trustedProxyCIDRs for the ALB subnets")
	}
	seen := map[string]struct{}{}
	for _, raw := range in.proxyCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.String() != raw || prefix != prefix.Masked() || !trustedPrefixAllowed(prefix) {
			return fmt.Errorf("trusted proxy CIDR %q must be a canonical network prefix", raw)
		}
		if _, ok := seen[raw]; ok {
			return fmt.Errorf("duplicate trusted proxy CIDR %q", raw)
		}
		seen[raw] = struct{}{}
	}
	if in.vpcID == "" || !strings.HasPrefix(in.vpcID, "vpc-") || !strings.HasPrefix(in.albSecurityGroupID, "sg-") {
		return errors.New("activate stage requires valid-looking supplied VPC and ALB security group IDs")
	}
	subnetSeen := map[string]struct{}{}
	for _, subnetID := range in.publicSubnetIDs {
		if !strings.HasPrefix(subnetID, "subnet-") {
			return errors.New("publicSubnetIds must contain subnet IDs")
		}
		if _, exists := subnetSeen[subnetID]; exists {
			return errors.New("publicSubnetIds must be distinct")
		}
		subnetSeen[subnetID] = struct{}{}
	}
	if !strings.HasPrefix(in.albListenerARN, fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/", in.region, in.accountID)) {
		return errors.New("albListenerArn must be an HTTPS listener ARN in the configured account and region")
	}
	if strings.ContainsAny(in.albDNSName, ":/@*? \r\n") || !strings.Contains(in.albDNSName, ".") {
		return errors.New("albDnsName must be a DNS name without scheme or path")
	}
	return nil
}

func validDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || strings.ToLower(domain) != domain || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func trustedPrefixAllowed(prefix netip.Prefix) bool {
	allowed := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("::1/128"),
	}
	for _, boundary := range allowed {
		if boundary.Addr().Is4() == prefix.Addr().Is4() && boundary.Bits() <= prefix.Bits() && boundary.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func deployHostedPilot(ctx *pulumi.Context, in inputs) error {
	repository, err := ecr.NewRepository(ctx, "ferro-hosted-images", &ecr.RepositoryArgs{
		Name:               pulumi.String(stackPrefix),
		ImageTagMutability: pulumi.String("IMMUTABLE"),
		ImageScanningConfiguration: &ecr.RepositoryImageScanningConfigurationArgs{
			ScanOnPush: pulumi.Bool(true),
		},
		EncryptionConfigurations: ecr.RepositoryEncryptionConfigurationArray{
			&ecr.RepositoryEncryptionConfigurationArgs{EncryptionType: pulumi.String("AES256")},
		},
	})
	if err != nil {
		return fmt.Errorf("create private image repository: %w", err)
	}
	ctx.Export("imageRepositoryUrl", repository.RepositoryUrl)

	certificate, err := acm.NewCertificate(ctx, "ferro-hosted-certificate", &acm.CertificateArgs{
		DomainName:       pulumi.String(in.publicDomain),
		ValidationMethod: pulumi.String("DNS"),
		Tags:             pulumi.StringMap{"Service": pulumi.String(stackPrefix)},
	}, pulumi.Protect(true), pulumi.RetainOnDelete(true))
	if err != nil {
		return fmt.Errorf("request retained DNS-validated certificate: %w", err)
	}
	ctx.Export("certificateArn", certificate.Arn)
	ctx.Export("certificateValidationOptions", certificate.DomainValidationOptions)
	if in.albDNSName != "" {
		ctx.Export("sharedALBDnsName", pulumi.String(in.albDNSName))
	}

	if in.createDeploymentIdentity {
		component, err := deploymentidentity.NewGitHubActionsDeploymentIdentity(ctx, identityComponent, deploymentidentity.Args{
			RoleNamePrefix:    in.identity.roleNamePrefix,
			ProviderARN:       in.identity.providerARN,
			Audience:          deploymentidentity.GitHubOIDCAudience,
			RepositoryOwner:   in.identity.owner,
			RepositoryName:    in.identity.repository,
			OwnerID:           in.identity.ownerID,
			RepositoryID:      in.identity.repositoryID,
			Ref:               in.identity.previewRef,
			ApplyEnvironment:  in.identity.applyEnvironment,
			PreviewPolicyJSON: in.identity.previewPolicyJSON,
			ApplyPolicyJSON:   in.identity.applyPolicyJSON,
		})
		if err != nil {
			return fmt.Errorf("create separated GitHub deployment identities: %w", err)
		}
		ctx.Export("githubPreviewRoleArn", component.PreviewRoleARN)
		ctx.Export("githubApplyRoleArn", component.ApplyRoleARN)
		ctx.Export("githubPreviewSubject", component.Subject)
		ctx.Export("githubApplySubject", component.ApplySubject)
	}

	if in.stage == "bootstrap" {
		return nil
	}
	return activateHostedPilot(ctx, in, repository, certificate)
}

func activateHostedPilot(ctx *pulumi.Context, in inputs, repository *ecr.Repository, certificate *acm.Certificate) error {
	validatedCertificate, err := acm.NewCertificateValidation(ctx, "ferro-hosted-certificate-validation", &acm.CertificateValidationArgs{
		CertificateArn:        certificate.Arn,
		ValidationRecordFqdns: pulumi.ToStringArray(in.validationRecordFQDNs),
	})
	if err != nil {
		return fmt.Errorf("wait for externally published certificate records: %w", err)
	}
	if _, err := lb.NewListenerCertificate(ctx, "ferro-hosted-listener-certificate", &lb.ListenerCertificateArgs{
		ListenerArn:    pulumi.String(in.albListenerARN),
		CertificateArn: validatedCertificate.CertificateArn,
	}); err != nil {
		return fmt.Errorf("attach certificate to supplied shared HTTPS listener: %w", err)
	}

	cluster, err := ecs.NewCluster(ctx, "ferro-hosted-cluster", &ecs.ClusterArgs{
		Name: pulumi.String(clusterName),
		Tags: pulumi.StringMap{"Service": pulumi.String(stackPrefix)},
	})
	if err != nil {
		return fmt.Errorf("create hosted ECS cluster: %w", err)
	}

	logGroup, err := cloudwatch.NewLogGroup(ctx, "ferro-hosted-logs", &cloudwatch.LogGroupArgs{
		Name:            pulumi.String(serviceLogGroup),
		RetentionInDays: pulumi.Int(14),
	})
	if err != nil {
		return fmt.Errorf("create 14-day service log group: %w", err)
	}
	wakeLogs, err := cloudwatch.NewLogGroup(ctx, "ferro-hosted-wake-logs", &cloudwatch.LogGroupArgs{
		Name:            pulumi.String(wakeLogGroup),
		RetentionInDays: pulumi.Int(14),
	})
	if err != nil {
		return fmt.Errorf("create 14-day wake log group: %w", err)
	}

	mcpSecret, bridgeSecret, mcpValue, bridgeValue, err := newCredentials(ctx)
	if err != nil {
		return err
	}

	serviceSG, efsSG, err := newSecurityGroups(ctx, in)
	if err != nil {
		return err
	}
	albEgressRule, err := ec2.NewSecurityGroupRule(ctx, "ferro-hosted-alb-egress", &ec2.SecurityGroupRuleArgs{
		Type: pulumi.String("egress"), Protocol: pulumi.String("tcp"), FromPort: pulumi.Int(listenerPort), ToPort: pulumi.Int(listenerPort),
		SecurityGroupId: pulumi.String(in.albSecurityGroupID), SourceSecurityGroupId: serviceSG.ID(),
		Description: pulumi.String("Allow the shared Ferro ALB to reach this service on its HTTP target port"),
	})
	if err != nil {
		return fmt.Errorf("allow the shared ALB to reach the dedicated Ferro task security group: %w", err)
	}
	fileSystem, accessPoint, err := newPrivateHome(ctx)
	if err != nil {
		return err
	}
	var mountTargets []pulumi.Resource
	for i, subnetID := range in.publicSubnetIDs {
		mountTarget, err := efs.NewMountTarget(ctx, fmt.Sprintf("ferro-hosted-efs-mount-%d", i+1), &efs.MountTargetArgs{
			FileSystemId:   fileSystem.ID(),
			SubnetId:       pulumi.String(subnetID),
			SecurityGroups: pulumi.StringArray{efsSG.ID()},
		})
		if err != nil {
			return fmt.Errorf("create EFS mount target for subnet %d: %w", i+1, err)
		}
		mountTargets = append(mountTargets, mountTarget)
	}

	taskRole, executionRole, err := newTaskRoles(ctx, in, repository, logGroup, mcpSecret, bridgeSecret, fileSystem, accessPoint)
	if err != nil {
		return err
	}
	efsPolicy, err := newFileSystemPolicy(ctx, fileSystem, accessPoint, taskRole)
	if err != nil {
		return err
	}

	wakeRole, err := newWakeRole(ctx, in, wakeLogs)
	if err != nil {
		return err
	}

	repoURI := repository.RepositoryUrl.ApplyT(func(url string) string { return url + "@" + in.imageDigest }).(pulumi.StringOutput)
	wake, err := newWakeFunction(ctx, in, wakeRole, bridgeValue)
	if err != nil {
		return err
	}
	wakeTarget, err := lb.NewTargetGroup(ctx, "ferro-hosted-wake-target", &lb.TargetGroupArgs{
		Name:                           pulumi.String("ferro-hosted-wake"),
		TargetType:                     pulumi.String("lambda"),
		LambdaMultiValueHeadersEnabled: pulumi.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("create ALB Lambda target group: %w", err)
	}
	wakePermission, err := lambda.NewPermission(ctx, "ferro-hosted-wake-alb-invoke", &lambda.PermissionArgs{
		Action:        pulumi.String("lambda:InvokeFunction"),
		Function:      wake.Name,
		Principal:     pulumi.String("elasticloadbalancing.amazonaws.com"),
		SourceArn:     wakeTarget.Arn,
		SourceAccount: pulumi.String(in.accountID),
	})
	if err != nil {
		return fmt.Errorf("grant the supplied ALB target group invoke access: %w", err)
	}
	if _, err := lb.NewTargetGroupAttachment(ctx, "ferro-hosted-wake-target-attachment", &lb.TargetGroupAttachmentArgs{
		TargetGroupArn: wakeTarget.Arn,
		TargetId:       wake.Arn,
	}, pulumi.DependsOn([]pulumi.Resource{wakePermission})); err != nil {
		return fmt.Errorf("register the wake function in its ALB target group: %w", err)
	}
	if _, err := lb.NewListenerRule(ctx, "ferro-hosted-wake-rule", &lb.ListenerRuleArgs{
		ListenerArn: pulumi.String(in.albListenerARN),
		Priority:    pulumi.Int(in.wakeRulePriority),
		Conditions: lb.ListenerRuleConditionArray{
			&lb.ListenerRuleConditionArgs{HostHeader: &lb.ListenerRuleConditionHostHeaderArgs{Values: pulumi.StringArray{pulumi.String(in.publicDomain)}}},
			&lb.ListenerRuleConditionArgs{PathPattern: &lb.ListenerRuleConditionPathPatternArgs{Values: pulumi.StringArray{pulumi.String("/bridge/wake")}}},
			&lb.ListenerRuleConditionArgs{HttpRequestMethod: &lb.ListenerRuleConditionHttpRequestMethodArgs{Values: pulumi.StringArray{pulumi.String("POST")}}},
		},
		Actions: lb.ListenerRuleActionArray{&lb.ListenerRuleActionArgs{Type: pulumi.String("forward"), TargetGroupArn: wakeTarget.Arn}},
	}); err != nil {
		return fmt.Errorf("create exact-host wake route: %w", err)
	}

	appTarget, err := lb.NewTargetGroup(ctx, "ferro-hosted-app-target", &lb.TargetGroupArgs{
		Name:       pulumi.String("ferro-hosted-app"),
		Port:       pulumi.Int(listenerPort),
		Protocol:   pulumi.String("HTTP"),
		TargetType: pulumi.String("ip"),
		VpcId:      pulumi.String(in.vpcID),
		HealthCheck: &lb.TargetGroupHealthCheckArgs{
			Enabled: pulumi.Bool(true), Protocol: pulumi.String("HTTP"), Port: pulumi.String("traffic-port"),
			Path: pulumi.String("/healthz"), Matcher: pulumi.String("200"), Interval: pulumi.Int(30), Timeout: pulumi.Int(5),
			HealthyThreshold: pulumi.Int(2), UnhealthyThreshold: pulumi.Int(2),
		},
	})
	if err != nil {
		return fmt.Errorf("create private service target group: %w", err)
	}
	appRule, err := lb.NewListenerRule(ctx, "ferro-hosted-app-rule", &lb.ListenerRuleArgs{
		ListenerArn: pulumi.String(in.albListenerARN),
		Priority:    pulumi.Int(in.appRulePriority),
		Conditions:  lb.ListenerRuleConditionArray{&lb.ListenerRuleConditionArgs{HostHeader: &lb.ListenerRuleConditionHostHeaderArgs{Values: pulumi.StringArray{pulumi.String(in.publicDomain)}}}},
		Actions:     lb.ListenerRuleActionArray{&lb.ListenerRuleActionArgs{Type: pulumi.String("forward"), TargetGroupArn: appTarget.Arn}},
	})
	if err != nil {
		return fmt.Errorf("create exact-host service route: %w", err)
	}

	containerDefs := pulumi.All(repoURI, mcpSecret.Arn, bridgeSecret.Arn, logGroup.Name).ApplyT(func(values []any) (string, error) {
		return taskContainerDefinitions(values, in)
	}).(pulumi.StringOutput)
	taskDef, err := ecs.NewTaskDefinition(ctx, "ferro-hosted-task", &ecs.TaskDefinitionArgs{
		Family: pulumi.String(stackPrefix),
		Cpu:    pulumi.String("512"), Memory: pulumi.String("1024"),
		NetworkMode: pulumi.String("awsvpc"), RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		RuntimePlatform:  &ecs.TaskDefinitionRuntimePlatformArgs{CpuArchitecture: pulumi.String("ARM64"), OperatingSystemFamily: pulumi.String("LINUX")},
		ExecutionRoleArn: executionRole.Arn, TaskRoleArn: taskRole.Arn, ContainerDefinitions: containerDefs,
		Volumes: ecs.TaskDefinitionVolumeArray{
			&ecs.TaskDefinitionVolumeArgs{Name: pulumi.String("private-home"), EfsVolumeConfiguration: &ecs.TaskDefinitionVolumeEfsVolumeConfigurationArgs{
				FileSystemId: fileSystem.ID(), RootDirectory: pulumi.String("/"), TransitEncryption: pulumi.String("ENABLED"),
				AuthorizationConfig: &ecs.TaskDefinitionVolumeEfsVolumeConfigurationAuthorizationConfigArgs{AccessPointId: accessPoint.ID(), Iam: pulumi.String("ENABLED")},
			}},
			&ecs.TaskDefinitionVolumeArgs{Name: pulumi.String("tmp")},
		},
	})
	if err != nil {
		return fmt.Errorf("register Fargate task definition: %w", err)
	}

	service, err := ecs.NewService(ctx, "ferro-hosted-service", &ecs.ServiceArgs{
		Name: pulumi.String(serviceName), Cluster: cluster.Arn, TaskDefinition: taskDef.Arn,
		LaunchType: pulumi.String("FARGATE"), SchedulingStrategy: pulumi.String("REPLICA"), DesiredCount: pulumi.Int(0),
		DeploymentMinimumHealthyPercent: pulumi.Int(0), DeploymentMaximumPercent: pulumi.Int(100),
		EnableExecuteCommand: pulumi.Bool(false), WaitForSteadyState: pulumi.Bool(false),
		NetworkConfiguration:     &ecs.ServiceNetworkConfigurationArgs{AssignPublicIp: pulumi.Bool(true), Subnets: pulumi.ToStringArray(in.publicSubnetIDs), SecurityGroups: pulumi.StringArray{serviceSG.ID()}},
		LoadBalancers:            ecs.ServiceLoadBalancerArray{&ecs.ServiceLoadBalancerArgs{TargetGroupArn: appTarget.Arn, ContainerName: pulumi.String(containerName), ContainerPort: pulumi.Int(listenerPort)}},
		DeploymentCircuitBreaker: &ecs.ServiceDeploymentCircuitBreakerArgs{Enable: pulumi.Bool(true), Rollback: pulumi.Bool(true)},
		Tags:                     pulumi.StringMap{"Service": pulumi.String(stackPrefix)},
	}, pulumi.IgnoreChanges([]string{"desiredCount"}), pulumi.Protect(true), pulumi.DependsOn(append(mountTargets, appRule, efsPolicy, albEgressRule)))
	if err != nil {
		return fmt.Errorf("create zero-desired single-task ECS service: %w", err)
	}
	ctx.Export("clusterArn", cluster.Arn)
	ctx.Export("serviceArn", service.ID())
	ctx.Export("fileSystemId", fileSystem.ID())
	ctx.Export("taskRoleArn", taskRole.Arn)
	ctx.Export("executionRoleArn", executionRole.Arn)
	ctx.Export("wakeFunctionArn", wake.Arn)
	ctx.Export("mcpTokenValue", pulumi.ToSecret(mcpValue))
	ctx.Export("bridgeTokenValue", pulumi.ToSecret(bridgeValue))
	return nil
}

func newCredentials(ctx *pulumi.Context) (*secretsmanager.Secret, *secretsmanager.Secret, pulumi.StringOutput, pulumi.StringOutput, error) {
	mcpPassword, err := random.NewRandomPassword(ctx, "ferro-hosted-mcp-token", &random.RandomPasswordArgs{
		Length: pulumi.Int(64), MinLower: pulumi.Int(1), MinUpper: pulumi.Int(1), MinNumeric: pulumi.Int(1), Special: pulumi.Bool(false),
	})
	if err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("generate MCP token: %w", err)
	}
	bridgePassword, err := random.NewRandomPassword(ctx, "ferro-hosted-bridge-token", &random.RandomPasswordArgs{
		Length: pulumi.Int(64), MinLower: pulumi.Int(1), MinUpper: pulumi.Int(1), MinNumeric: pulumi.Int(1), Special: pulumi.Bool(false),
	})
	if err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("generate bridge token: %w", err)
	}
	mcp, err := secretsmanager.NewSecret(ctx, "ferro-hosted-mcp-secret", &secretsmanager.SecretArgs{Name: pulumi.String(mcpSecretName)})
	if err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("create MCP Secrets Manager entry: %w", err)
	}
	if _, err := secretsmanager.NewSecretVersion(ctx, "ferro-hosted-mcp-secret-version", &secretsmanager.SecretVersionArgs{SecretId: mcp.ID(), SecretString: pulumi.ToSecret(mcpPassword.Result).(pulumi.StringOutput).ToStringPtrOutput()}); err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("set generated MCP secret value: %w", err)
	}
	bridge, err := secretsmanager.NewSecret(ctx, "ferro-hosted-bridge-secret", &secretsmanager.SecretArgs{Name: pulumi.String(bridgeSecretName)})
	if err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("create bridge Secrets Manager entry: %w", err)
	}
	if _, err := secretsmanager.NewSecretVersion(ctx, "ferro-hosted-bridge-secret-version", &secretsmanager.SecretVersionArgs{SecretId: bridge.ID(), SecretString: pulumi.ToSecret(bridgePassword.Result).(pulumi.StringOutput).ToStringPtrOutput()}); err != nil {
		return nil, nil, pulumi.StringOutput{}, pulumi.StringOutput{}, fmt.Errorf("set generated bridge secret value: %w", err)
	}
	return mcp, bridge, pulumi.ToSecret(mcpPassword.Result).(pulumi.StringOutput), pulumi.ToSecret(bridgePassword.Result).(pulumi.StringOutput), nil
}

func newSecurityGroups(ctx *pulumi.Context, in inputs) (*ec2.SecurityGroup, *ec2.SecurityGroup, error) {
	taskSG, err := ec2.NewSecurityGroup(ctx, "ferro-hosted-task-sg", &ec2.SecurityGroupArgs{
		Name: pulumi.String("ferro-hosted-task"), Description: pulumi.String("Private hosted task ingress from the supplied shared ALB only"), VpcId: pulumi.String(in.vpcID),
		Ingress: ec2.SecurityGroupIngressArray{&ec2.SecurityGroupIngressArgs{Protocol: pulumi.String("tcp"), FromPort: pulumi.Int(listenerPort), ToPort: pulumi.Int(listenerPort), SecurityGroups: pulumi.StringArray{pulumi.String(in.albSecurityGroupID)}}},
		Egress:  ec2.SecurityGroupEgressArray{&ec2.SecurityGroupEgressArgs{Protocol: pulumi.String("-1"), FromPort: pulumi.Int(0), ToPort: pulumi.Int(0), CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")}}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create task security group: %w", err)
	}
	efsSG, err := ec2.NewSecurityGroup(ctx, "ferro-hosted-efs-sg", &ec2.SecurityGroupArgs{
		Name: pulumi.String("ferro-hosted-efs"), Description: pulumi.String("NFS ingress only from the hosted task security group"), VpcId: pulumi.String(in.vpcID),
		Ingress: ec2.SecurityGroupIngressArray{&ec2.SecurityGroupIngressArgs{Protocol: pulumi.String("tcp"), FromPort: pulumi.Int(2049), ToPort: pulumi.Int(2049), SecurityGroups: pulumi.StringArray{taskSG.ID()}}},
		Egress:  ec2.SecurityGroupEgressArray{},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create EFS security group: %w", err)
	}
	return taskSG, efsSG, nil
}

func newPrivateHome(ctx *pulumi.Context) (*efs.FileSystem, *efs.AccessPoint, error) {
	fs, err := efs.NewFileSystem(ctx, "ferro-hosted-private-home", &efs.FileSystemArgs{
		Encrypted: pulumi.Bool(true), PerformanceMode: pulumi.String("generalPurpose"), ThroughputMode: pulumi.String("bursting"),
		Tags: pulumi.StringMap{"Service": pulumi.String(stackPrefix), "DataClass": pulumi.String("private-state")},
	}, pulumi.Protect(true))
	if err != nil {
		return nil, nil, fmt.Errorf("create retained encrypted private home: %w", err)
	}
	accessPoint, err := efs.NewAccessPoint(ctx, "ferro-hosted-private-home-ap", &efs.AccessPointArgs{
		FileSystemId:  fs.ID(),
		PosixUser:     &efs.AccessPointPosixUserArgs{Uid: pulumi.Int(65532), Gid: pulumi.Int(65532)},
		RootDirectory: &efs.AccessPointRootDirectoryArgs{Path: pulumi.String("/ferro-private-home"), CreationInfo: &efs.AccessPointRootDirectoryCreationInfoArgs{OwnerGid: pulumi.Int(65532), OwnerUid: pulumi.Int(65532), Permissions: pulumi.String("0700")}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create UID 65532 private home access point: %w", err)
	}
	return fs, accessPoint, nil
}

type policyStatement struct {
	Sid       string                       `json:"Sid,omitempty"`
	Effect    string                       `json:"Effect"`
	Action    []string                     `json:"Action"`
	Resource  []string                     `json:"Resource"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

func policyJSON(statements ...policyStatement) (string, error) {
	for i := range statements {
		sort.Strings(statements[i].Action)
		sort.Strings(statements[i].Resource)
	}
	return marshalIAM(map[string]any{"Version": "2012-10-17", "Statement": statements})
}

func marshalIAM(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func arn(region, account, service, resource string) string {
	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", service, region, account, resource)
}

func efsClientPolicy(fileSystemARN, accessPointARN string) (string, error) {
	if fileSystemARN == "" || accessPointARN == "" {
		return "", errors.New("EFS filesystem and access point ARNs are required")
	}
	return policyJSON(policyStatement{
		Effect:   "Allow",
		Action:   []string{"elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite"},
		Resource: []string{fileSystemARN},
		Condition: map[string]map[string]string{
			"StringEquals": {"elasticfilesystem:AccessPointArn": accessPointARN},
		},
	})
}

func efsFileSystemPolicy(fileSystemARN, accessPointARN, taskRoleARN string) (string, error) {
	if fileSystemARN == "" || accessPointARN == "" || taskRoleARN == "" {
		return "", errors.New("EFS filesystem, access point, and task role ARNs are required")
	}
	resource := []string{fileSystemARN}
	return marshalIAM(map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{"Sid": "AllowTaskRoleViaAccessPoint", "Effect": "Allow", "Principal": map[string]string{"AWS": taskRoleARN}, "Action": []string{"elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite"}, "Resource": resource, "Condition": map[string]any{"StringEquals": map[string]string{"elasticfilesystem:AccessPointArn": accessPointARN}}},
		},
	})
}

func newFileSystemPolicy(ctx *pulumi.Context, fileSystem *efs.FileSystem, accessPoint *efs.AccessPoint, taskRole *iam.Role) (*efs.FileSystemPolicy, error) {
	policy := pulumi.All(fileSystem.Arn, accessPoint.Arn, taskRole.Arn).ApplyT(func(values []any) (string, error) {
		fileSystemARN, fsOK := values[0].(string)
		accessPointARN, apOK := values[1].(string)
		taskRoleARN, roleOK := values[2].(string)
		if !fsOK || !apOK || !roleOK {
			return "", errors.New("EFS policy inputs have invalid types")
		}
		return efsFileSystemPolicy(fileSystemARN, accessPointARN, taskRoleARN)
	}).(pulumi.StringOutput)
	resource, err := efs.NewFileSystemPolicy(ctx, "ferro-hosted-private-home-policy", &efs.FileSystemPolicyArgs{
		FileSystemId: fileSystem.ID(), Policy: policy, BypassPolicyLockoutSafetyCheck: pulumi.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("enforce private-home EFS role, access point, and TLS policy: %w", err)
	}
	return resource, nil
}

func trustPolicy(servicePrincipal, region, account string) (string, error) {
	statement := map[string]any{
		"Effect": "Allow", "Principal": map[string]string{"Service": servicePrincipal}, "Action": "sts:AssumeRole",
	}
	switch servicePrincipal {
	case "ecs-tasks.amazonaws.com":
		statement["Condition"] = map[string]any{
			"StringEquals": map[string]string{"aws:SourceAccount": account},
			"ArnLike":      map[string]string{"aws:SourceArn": fmt.Sprintf("arn:aws:ecs:%s:%s:*", region, account)},
		}
	case "lambda.amazonaws.com":
		// Lambda assumes execution roles without aws:SourceArn or aws:SourceAccount
		// in the request context; these are not valid trust-policy conditions here.
	default:
		return "", fmt.Errorf("unsupported service principal %q", servicePrincipal)
	}
	return marshalIAM(map[string]any{"Version": "2012-10-17", "Statement": []any{statement}})
}

func newTaskRoles(ctx *pulumi.Context, in inputs, repository *ecr.Repository, logGroup *cloudwatch.LogGroup, mcpSecret, bridgeSecret *secretsmanager.Secret, fileSystem *efs.FileSystem, accessPoint *efs.AccessPoint) (*iam.Role, *iam.Role, error) {
	trust, err := trustPolicy("ecs-tasks.amazonaws.com", in.region, in.accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("encode ECS task trust: %w", err)
	}
	taskRole, err := iam.NewRole(ctx, "ferro-hosted-task-role", &iam.RoleArgs{Name: pulumi.String("ferro-hosted-task"), AssumeRolePolicy: pulumi.String(trust)})
	if err != nil {
		return nil, nil, fmt.Errorf("create exact-scope ECS task role: %w", err)
	}
	executionRole, err := iam.NewRole(ctx, "ferro-hosted-execution-role", &iam.RoleArgs{Name: pulumi.String("ferro-hosted-execution"), AssumeRolePolicy: pulumi.String(trust)})
	if err != nil {
		return nil, nil, fmt.Errorf("create ECS execution role: %w", err)
	}
	serviceARN := arn(in.region, in.accountID, "ecs", "service/"+clusterName+"/"+serviceName)
	clusterARN := arn(in.region, in.accountID, "ecs", "cluster/"+clusterName)
	taskProtectionARN := arn(in.region, in.accountID, "ecs", "task/"+clusterName+"/*")
	fsArn := fileSystem.Arn
	apArn := accessPoint.Arn
	fsPolicy := pulumi.All(fsArn, apArn).ApplyT(func(values []any) (string, error) {
		filesystemARN, ok := values[0].(string)
		if !ok || filesystemARN == "" {
			return "", errors.New("EFS filesystem ARN is missing")
		}
		accessPointARN, ok := values[1].(string)
		if !ok || accessPointARN == "" {
			return "", errors.New("EFS access point ARN is missing")
		}
		return efsClientPolicy(filesystemARN, accessPointARN)
	}).(pulumi.StringOutput)
	taskPolicy, err := policyJSON(
		policyStatement{Sid: "SleepExactService", Effect: "Allow", Action: []string{"ecs:UpdateService"}, Resource: []string{serviceARN}},
		policyStatement{Sid: "ProtectTasksInCluster", Effect: "Allow", Action: []string{"ecs:GetTaskProtection", "ecs:UpdateTaskProtection"}, Resource: []string{taskProtectionARN}, Condition: map[string]map[string]string{"ArnEquals": {"ecs:cluster": clusterARN}}},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("encode task policy: %w", err)
	}
	taskPolicyWithEFS := fsPolicy.ApplyT(func(efsPolicy string) (string, error) { return combinePolicies(taskPolicy, efsPolicy) }).(pulumi.StringOutput)
	if _, err := iam.NewRolePolicy(ctx, "ferro-hosted-task-policy", &iam.RolePolicyArgs{Role: taskRole.Name, Policy: taskPolicyWithEFS}); err != nil {
		return nil, nil, fmt.Errorf("attach task role service and EFS permissions: %w", err)
	}
	repositoryARN := repository.Arn
	logARN := logGroup.Arn.ApplyT(func(value string) string { return value + ":*" }).(pulumi.StringOutput)
	secretsARNs := pulumi.All(mcpSecret.Arn, bridgeSecret.Arn).ApplyT(func(values []any) []string { return []string{values[0].(string), values[1].(string)} }).(pulumi.StringArrayOutput)
	executionPolicy := pulumi.All(repositoryARN, logARN, secretsARNs).ApplyT(func(values []any) (string, error) {
		repoArn := values[0].(string)
		logsArn := values[1].(string)
		secretARNs := values[2].([]string)
		return policyJSON(policyStatement{Sid: "ReadPinnedImage", Effect: "Allow", Action: []string{"ecr:BatchCheckLayerAvailability", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"}, Resource: []string{repoArn}}, policyStatement{Sid: "ECRAuthorization", Effect: "Allow", Action: []string{"ecr:GetAuthorizationToken"}, Resource: []string{"*"}, Condition: map[string]map[string]string{"StringEquals": {"aws:RequestedRegion": in.region}}}, policyStatement{Sid: "WriteOwnLogs", Effect: "Allow", Action: []string{"logs:CreateLogStream", "logs:PutLogEvents"}, Resource: []string{logsArn}}, policyStatement{Sid: "ReadOnlyInjectedSecrets", Effect: "Allow", Action: []string{"secretsmanager:GetSecretValue"}, Resource: secretARNs})
	}).(pulumi.StringOutput)
	if _, err := iam.NewRolePolicy(ctx, "ferro-hosted-execution-policy", &iam.RolePolicyArgs{Role: executionRole.Name, Policy: executionPolicy}); err != nil {
		return nil, nil, fmt.Errorf("attach exact ECR, logs, and secrets execution permissions: %w", err)
	}
	return taskRole, executionRole, nil
}

func combinePolicies(left, right string) (string, error) {
	var a, b struct {
		Version   string
		Statement []policyStatement
	}
	if err := json.Unmarshal([]byte(left), &a); err != nil {
		return "", fmt.Errorf("decode task policy: %w", err)
	}
	if err := json.Unmarshal([]byte(right), &b); err != nil {
		return "", fmt.Errorf("decode EFS policy: %w", err)
	}
	if a.Version != "2012-10-17" || b.Version != a.Version {
		return "", errors.New("IAM policy versions do not match")
	}
	a.Statement = append(a.Statement, b.Statement...)
	return marshalIAM(a)
}

func newWakeRole(ctx *pulumi.Context, in inputs, logGroup *cloudwatch.LogGroup) (*iam.Role, error) {
	trust, err := trustPolicy("lambda.amazonaws.com", in.region, in.accountID)
	if err != nil {
		return nil, fmt.Errorf("encode Lambda trust: %w", err)
	}
	role, err := iam.NewRole(ctx, "ferro-hosted-wake-role", &iam.RoleArgs{Name: pulumi.String("ferro-hosted-wake"), AssumeRolePolicy: pulumi.String(trust)})
	if err != nil {
		return nil, fmt.Errorf("create wake function role: %w", err)
	}
	serviceARN := arn(in.region, in.accountID, "ecs", "service/"+clusterName+"/"+serviceName)
	logARN := logGroup.Arn.ApplyT(func(value string) string { return value + ":*" }).(pulumi.StringOutput)
	policy := pulumi.All(logARN).ApplyT(func(values []any) (string, error) {
		return policyJSON(policyStatement{Sid: "WakeOnlyThisService", Effect: "Allow", Action: []string{"ecs:UpdateService"}, Resource: []string{serviceARN}}, policyStatement{Sid: "OwnFunctionLogs", Effect: "Allow", Action: []string{"logs:CreateLogStream", "logs:PutLogEvents"}, Resource: []string{values[0].(string)}})
	}).(pulumi.StringOutput)
	if _, err := iam.NewRolePolicy(ctx, "ferro-hosted-wake-policy", &iam.RolePolicyArgs{Role: role.Name, Policy: policy}); err != nil {
		return nil, fmt.Errorf("attach exact wake and logs permissions: %w", err)
	}
	return role, nil
}

func newWakeFunction(ctx *pulumi.Context, in inputs, role *iam.Role, bridgeToken pulumi.StringOutput) (*lambda.Function, error) {
	function, err := lambda.NewFunction(ctx, "ferro-hosted-wake-function", &lambda.FunctionArgs{
		Name: pulumi.String(wakeFunctionName), Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"),
		Architectures: pulumi.StringArray{pulumi.String("arm64")}, Code: pulumi.NewFileArchive(in.wakeZipPath), Role: role.Arn,
		Timeout: pulumi.Int(12), MemorySize: pulumi.Int(128),
		Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"BRIDGE_TOKEN": bridgeToken, "SERVICE_ARN": pulumi.String(arn(in.region, in.accountID, "ecs", "service/"+clusterName+"/"+serviceName)), "PUBLIC_HOST": pulumi.String(in.publicDomain)}},
	})
	if err != nil {
		return nil, fmt.Errorf("create bounded Go custom-runtime wake function: %w", err)
	}
	return function, nil
}

func taskContainerDefinitions(values []any, in inputs) (string, error) {
	if len(values) != 4 {
		return "", fmt.Errorf("container values count %d, want 4", len(values))
	}
	repoURI, mcpARN, bridgeARN, logName := values[0].(string), values[1].(string), values[2].(string), values[3].(string)
	env := []map[string]string{
		{"name": "FERRO_CLOUD_LISTEN_ADDR", "value": "0.0.0.0:8080"},
		{"name": "FERRO_CLOUD_PUBLIC_ORIGIN", "value": "https://" + in.publicDomain},
		{"name": "FERRO_CLOUD_TRUSTED_PROXY_CIDRS", "value": strings.Join(in.proxyCIDRs, ",")},
		{"name": "AWS_REGION", "value": in.region},
		{"name": "FERRO_MCP_HOME", "value": privateHomePath},
		{"name": "FERRO_CLOUD_ECS_SERVICE_ARN", "value": arn(in.region, in.accountID, "ecs", "service/"+clusterName+"/"+serviceName)},
	}
	definition := []map[string]any{{
		"name": containerName, "image": repoURI, "essential": true, "user": "65532:65532", "readonlyRootFilesystem": true,
		"portMappings": []map[string]any{{"containerPort": listenerPort, "hostPort": listenerPort, "protocol": "tcp"}},
		"environment":  env, "secrets": []map[string]string{{"name": "FERRO_CLOUD_MCP_TOKEN_VALUE", "valueFrom": mcpARN}, {"name": "FERRO_CLOUD_BRIDGE_TOKEN_VALUE", "valueFrom": bridgeARN}},
		"mountPoints":      []map[string]any{{"sourceVolume": "private-home", "containerPath": privateHomePath, "readOnly": false}, {"sourceVolume": "tmp", "containerPath": "/tmp", "readOnly": false}},
		"logConfiguration": map[string]any{"logDriver": "awslogs", "options": map[string]string{"awslogs-group": logName, "awslogs-region": in.region, "awslogs-stream-prefix": "ferro"}},
	}}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("encode task container definition: %w", err)
	}
	return string(encoded), nil
}
