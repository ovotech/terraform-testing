package testhelpers

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrTypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// GetSsmParameter returns the value of the given parameter as stored in SSM
// Parameter Store. It will fail the test if a parameter is not found.
func GetSsmParameter(t *testing.T, name string) string {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("eu-west-1"))
	if err != nil {
		t.Fatalf("Error initialising AWS config: %s", err)
	}

	ssmClient := ssm.NewFromConfig(cfg)
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: true,
	})
	if err != nil {
		t.Fatalf("Error fetching SSM parameter: %s", err)
	}

	return aws.ToString(out.Parameter.Value)
}

// GetEcrRepository returns the given ECR repository
func GetEcrRepository(t *testing.T, name string) ecrTypes.Repository {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("eu-west-1"))
	if err != nil {
		t.Fatalf("Error initialising AWS config: %s", err)
	}

	ecrClient := ecr.NewFromConfig(cfg)
	out, err := ecrClient.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{name},
	})
	if err != nil {
		t.Fatalf("Error fetching ECR repository: %s", err)
	}

	if len(out.Repositories) == 0 {
		t.Fatalf("Could not find ECR repository %s", name)
	}

	return out.Repositories[0]
}
