package testhelpers

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
	teststructure "github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/mpvl/unique"
)

var blockedTerraformVersions = []string{"1.10.0"}

// Version represents a semantic version
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion converts a semantic version string to a Version struct
func parseVersion(v string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 3 {
		return Version{}, fmt.Errorf("invalid semantic version format: %s", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid major version: %s", parts[0])
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	return Version{
		Major: major,
		Minor: minor,
		Patch: 0,
	}, nil
}

// FilterMinorVersionsE groups versions by their major.minor version or
// returns an error if an error occurs during filtering
func FilterMinorVersionsE(versions []string) ([]string, error) {
	var minorVersions []string
	for _, v := range versions {
		parsed, err := parseVersion(v)
		if err != nil {
			return nil, err
		}
		minorKey := fmt.Sprintf("%d.%d.%d", parsed.Major, parsed.Minor, parsed.Patch)
		minorVersions = append(minorVersions, minorKey)
	}
	sort.Strings(minorVersions)
	unique.Strings(&minorVersions)
	return minorVersions, nil
}

// FilterMinorVersions groups versions by their major.minor version or
// fails the test if an error occurs during filtering
func FilterMinorVersions(t *testing.T, versions []string) []string {
	versions, err := FilterMinorVersionsE(versions)
	if err != nil {
		t.Fatalf(err.Error())
	}
	return versions
}

// GetTerraformVersionConstraintE returns the Terraform version string for the given module
// or an error if the provider cannot be found
func GetTerraformVersionConstraintE(srcDir string) (string, error) {
	files, err := os.ReadDir(srcDir)
	if err != nil {
		return "", err
	}

	vRegexp := regexp.MustCompile("required_version\\s*=\\s*\"([^\"]+)\"")

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if !strings.HasSuffix(file.Name(), ".tf") {
			continue
		}

		filename := fmt.Sprintf("%s/%s", srcDir, file.Name())
		content, err := os.ReadFile(filename)
		if err != nil {
			return "", err
		}

		f, diag := hclwrite.ParseConfig(content, file.Name(), hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return "", errors.New(diag.Error())
		}

		for _, block := range f.Body().Blocks() {
			if block.Type() != "terraform" {
				continue
			}

			requiredVersionSetting := block.Body().GetAttribute("required_version")
			if requiredVersionSetting == nil {
				continue
			}

			val := requiredVersionSetting.BuildTokens(nil).Bytes()
			constraint := vRegexp.FindSubmatch(val)

			if constraint == nil || len(constraint) < 2 {
				continue
			}

			return string(constraint[1]), nil
		}
	}

	return "", fmt.Errorf("required_version setting not found")
}

// GetTerraformVersionConstraint returns the Terraform version string for the given module
// or fails the test if the version is not found
func GetTerraformVersionConstraint(t *testing.T, srcDir string) string {
	constraint, err := GetTerraformVersionConstraintE(srcDir)
	if err != nil {
		t.Fatalf(err.Error())
	}
	return constraint
}

func newTerraformOptions(t *testing.T) *terraform.Options {
	t.Helper()

	// Start with default retryable errors as a baseline.
	opts := terraform.WithDefaultRetryableErrors(t, &terraform.Options{})

	// Add a pattern to cover off this corner case.
	opts.RetryableTerraformErrors[".*text file busy.*"] = "os: StartProcess ETXTBSY race on Unix systems - " +
		"https://github.com/golang/go/issues/22315"

	// Set some additional options to govern the retry behaviour.
	opts.MaxRetries = 3
	opts.TimeBetweenRetries = time.Second * 5

	return opts
}

func filterBlockedTerraformVersion(available []string) []string {
	slices.Sort(available)
	var filteredAvailableVersions []string

	for _, blockedVersion := range blockedTerraformVersions {
		n, found := slices.BinarySearch(available, blockedVersion)
		if found {
			filteredAvailableVersions = slices.Delete(available, n, n+1)
		}
	}
	return filteredAvailableVersions
}

func TerraformVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	constraint := GetTerraformVersionConstraint(t, srcDir)
	available := GetAvailableVersions(t, "terraform")
	filteredAvailable := filterBlockedTerraformVersion(available)
	versions := GetMatchingVersions(t, constraint, filteredAvailable)

	for _, version := range versions {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}
		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, "")
			UpdateModuleSourcesToLocalPaths(t, dst)
			binaryPath := DownloadTerraformVersion(t, version)
			tfOptions.TerraformDir = dst
			tfOptions.TerraformBinary = binaryPath
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}

func AwsProviderVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	constraint := GetProviderConstraint(t, srcDir, "aws")
	available := GetAvailableVersions(t, "terraform-provider-aws")
	versions := GetMatchingVersions(t, constraint, available)

	for _, version := range versions {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}

		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, "")
			UpdateModuleSourcesToLocalPaths(t, dst)
			UpdateProviderVersion(t, dst, "aws", version, "hashicorp/aws")
			tfOptions.TerraformDir = dst
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}

func CloudflareProviderVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	constraint := GetProviderConstraint(t, srcDir, "cloudflare")
	available := GetAvailableVersions(t, "terraform-provider-cloudflare")
	testVers := GetMatchingVersions(t, constraint, available)

	for _, version := range testVers {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}
		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, ".")
			UpdateModuleSourcesToLocalPaths(t, dst)
			UpdateProviderVersion(t, dst, "cloudflare", version, "cloudflare/cloudflare")
			tfOptions.TerraformDir = dst
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}

func DatadogProviderVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	constraint := GetProviderConstraint(t, "..", "datadog")
	available := GetAvailableVersions(t, "terraform-provider-datadog")
	testVers := GetMatchingVersions(t, constraint, available)

	for _, version := range testVers {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}
		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, ".")
			UpdateModuleSourcesToLocalPaths(t, dst)
			UpdateProviderVersion(t, dst, "datadog", version, "datadog/datadog")
			tfOptions.TerraformDir = dst
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}

func OpsgenieProviderVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	// Raised issue with OpsGenie https://github.com/opsgenie/terraform-provider-opsgenie/issues/367
	testVers := []string{"0.6.10", "0.6.11", "0.6.14", "0.6.15", "0.6.16", "0.6.17", "0.6.18", "0.6.19", "0.6.20"} // testing for specific versions as https://api.releases.hashicorp.com/v1/releases/terraform-provider-opsgenie is not showing anything newer than 0.6.11 currently

	for _, version := range testVers {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}
		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, ".")
			UpdateModuleSourcesToLocalPaths(t, dst)
			UpdateProviderVersion(t, dst, "opsgenie", version, "opsgenie/opsgenie")
			tfOptions.TerraformDir = dst
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}

func GcpProviderVersionsTest(t *testing.T, srcDir string, variables map[string]interface{}, environment_variables map[string]string) {
	constraint := GetProviderConstraint(t, "..", "google")
	available := GetAvailableVersions(t, "terraform-provider-google")
	testVers := GetMatchingVersions(t, constraint, available)

	for _, version := range testVers {
		tfOptions := newTerraformOptions(t)

		if len(variables) > 0 {
			tfOptions.Vars = variables
		}
		if len(environment_variables) > 0 {
			tfOptions.EnvVars = environment_variables
		}
		version := version
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			dst := teststructure.CopyTerraformFolderToTemp(t, srcDir, ".")
			UpdateModuleSourcesToLocalPaths(t, dst)
			UpdateProviderVersion(t, dst, "google", version, "hashicorp/google")
			tfOptions.TerraformDir = dst
			terraform.InitAndPlan(t, tfOptions)
		})
	}
}
