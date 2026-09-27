package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/sirerun/ferro/deploy/cloud/wake"
)

func main() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString("wake function configuration or initialization failed\n")
		os.Exit(1)
	}
}

func run() error {
	token, serviceARN, publicHost := os.Getenv("BRIDGE_TOKEN"), os.Getenv("SERVICE_ARN"), os.Getenv("PUBLIC_HOST")
	awsConfig, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return err
	}
	handler, err := wake.NewHandler(token, serviceARN, publicHost, ecs.NewFromConfig(awsConfig))
	if err != nil {
		return err
	}
	lambda.Start(handler)
	return nil
}
