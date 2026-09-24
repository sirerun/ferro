package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	if err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "hosted")
		inputs, err := loadInputs(ctx, cfg)
		if err != nil {
			return fmt.Errorf("load hosted pilot configuration: %w", err)
		}
		return deployHostedPilot(ctx, inputs)
	}); err != nil {
		panic(err)
	}
}
