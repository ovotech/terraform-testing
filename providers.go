package testhelpers

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// GetProviderConstraintE returns the version string for the given provider
// or an error if the provider cannot be found
func GetProviderConstraintE(srcDir, provider string) (string, error) {
	files, err := os.ReadDir(srcDir)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	vRegexp := regexp.MustCompile("version\\s*=\\s*\"([^\"]+)\"")

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if !strings.HasSuffix(file.Name(), ".tf") {
			continue
		}

		filename := fmt.Sprintf("%s/%s", srcDir, file.Name())
		content, err := ioutil.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("Error: %s", err)
		}

		f, diag := hclwrite.ParseConfig(content, file.Name(), hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return "", errors.New(diag.Error())
		}

		for _, block := range f.Body().Blocks() {
			if block.Type() != "terraform" {
				continue
			}

			for _, block := range block.Body().Blocks() {
				if block.Type() != "required_providers" {
					continue
				}

				provMap := block.Body().GetAttribute(provider)
				if provMap == nil {
					continue
				}

				val := provMap.BuildTokens(nil).Bytes()
				constraint := vRegexp.FindSubmatch(val)

				if constraint == nil || len(constraint) < 2 {
					continue
				}

				return string(constraint[1]), nil
			}
		}
	}

	return "", fmt.Errorf("provider %s not found", provider)
}

// GetProviderConstraint returns the version string for the given provider or
// fails the test if the provider is not found
func GetProviderConstraint(t *testing.T, srcDir, provider string) string {
	constraint, err := GetProviderConstraintE(srcDir, provider)
	if err != nil {
		t.Fatalf(err.Error())
	}
	return constraint
}

// GetBinaryUrlE will return the correct download URL for the provider binary version requested
// based on the operating system and architecture by fetching it from the Hashicorp releases API
//
// Usage:
// * version is the version of provider to download.
func GetBinaryUrl(version string, providerName string) (string, error) {
	var binaryUrl string
	operatingSystem := runtime.GOOS
	architecture := runtime.GOARCH
	releasesApiReq := fmt.Sprintf("https://api.releases.hashicorp.com/v1/releases/terraform-provider-"+providerName+"/%s", version)
	resp, err := http.Get(releasesApiReq)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	var result struct {
		Builds []struct {
			Arch string `json:"arch"`
			Os   string `json:"os"`
			Url  string `json:"url"`
		}
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	for _, res := range result.Builds {
		if res.Arch == architecture && res.Os == operatingSystem {
			binaryUrl = res.Url
		}
	}

	if len(binaryUrl) > 0 {
		return binaryUrl, nil
	} else {
		return "", errors.New("Unable to find an appropriate binary download URL for the underlying OS and architecture")
	}
}

// GetBinaryPath will return the cache path required to store the provider cache
//
// Usage:
// * cachePath is the path required to store the provider cache
func GetBinaryPath() (cachePath string) {
	homeDirectory, _ := os.UserHomeDir()
	binaryPath := filepath.Join(homeDirectory, ".terraform.d/plugin-cache")
	return binaryPath
}

// DownloadProviderVersionE will download the specified version of the provider into the ~/.terraform.d/plugin-cache directory
// Usage:
// * version is the version of provider to download.
// * sourceAddress is the sourceAddress of provider to download.
// * providerName is the name of provider to download.
func DownloadProviderVersionE(version string, sourceAddress string, providerName string) (binaryPath string, err error) {
	operatingSystem := runtime.GOOS
	architecture := runtime.GOARCH
	// Initialise all path variables
	binaryDownloadDirectory := GetBinaryPath()
	binaryPath = binaryDownloadDirectory + "/registry.terraform.io/" + sourceAddress + "/" + version
	// Don't do anything if the required binary already exists
	_, err = os.Stat(binaryPath)
	if err == nil {
		return binaryPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("unexpected error: %w", err)
	}
	// Create ~/.terraform.d/plugin-cache directory if it doesn't exist
	// https://gist.github.com/ivanzoid/5040166bb3f0c82575b52c2ca5f5a60c
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		os.MkdirAll(binaryPath, os.ModeDir|0755)
	}
	var binaryUrl string
	binaryUrl, err = GetBinaryUrl(version, providerName)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	req := fmt.Sprintf(binaryUrl)

	resp, err := http.Get(req)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", nil
	}

	// Create the file
	out, err := os.Create("/tmp/" + version + "_binary.zip")
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	// Sample code to extract zip file taken from https://stackoverflow.com/questions/20357223/easy-way-to-unzip-file-with-golang
	r, err := zip.OpenReader(out.Name())
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}

	// Cleanup temp directories
	defer func() {
		if tempErr := r.Close(); tempErr != nil {
			err = tempErr
		}
	}()
	defer os.Remove(out.Name())
	zipExtractPath := binaryDownloadDirectory + "/bin_" + version
	os.MkdirAll(zipExtractPath, 0755)

	// Cleanup zip files
	defer os.RemoveAll(zipExtractPath)
	for _, f := range r.File {
		err := extractAndWriteFile(zipExtractPath, f)
		if err != nil {
			return "", fmt.Errorf("Error: %s", err)
		}
	}

	// Move file from temporarily extracted location
	oldBinaryLocation := zipExtractPath + "/"
	err = os.Rename(oldBinaryLocation, binaryPath+"/"+operatingSystem+"_"+architecture)
	if err != nil {
		return "", fmt.Errorf("Error: %s", err)
	}
	return binaryDownloadDirectory, nil
}

// DownloadProviderVersion will download the specified version of provider into the ~/.terraform.d/plugin-cache directory.
//
// Usage:
// * version is the version of provider to download.
// * sourceAddress is the sourceAddress of provider to download.
// * providerName is the name of provider to download.
func DownloadProviderVersion(t *testing.T, version string, sourceAddress string, providerName string) string {
	binaryPath, err := DownloadProviderVersionE(version, sourceAddress, providerName)
	if err != nil {
		t.Fatalf(err.Error())
	}

	return binaryPath
}

// DownloadRequiredProviders will download the specified version of provider into the ~/.terraform.d/plugin-cache directory.
//
// Usage:
// * provider is the name of provider to download.
func DownloadRequiredProviders(t *testing.T, srcDir string, provider string) {
	constraint := GetProviderConstraint(t, srcDir, provider)
	available := GetAvailableVersions(t, "terraform-provider-"+provider)
	testVers := GetMatchingVersions(t, constraint, available)
	sourceAddress := GetSourceAddress(t, srcDir, provider)
	for _, version := range testVers {
		version := version
		DownloadProviderVersion(t, version, sourceAddress, provider)
	}
}

// GetSourceAddress returns the source string for the given provider or
// fails the test if the provider is not found
// Usage:
// * provider is the name of provider to download.
func GetSourceAddress(t *testing.T, srcDir, provider string) string {
	constraint, err := GetSourceAddressE(srcDir, provider, "source")
	if err != nil {
		t.Fatalf(err.Error())
	}
	return constraint
}

// GetSourceAddressE returns the source string for the given provider
// or an error if the provider cannot be found
// Usage:
// * attrribute is the name of attrribute to return.
func GetSourceAddressE(srcDir, provider string, attrribute string) (string, error) {
	files, err := os.ReadDir(srcDir)
	if err != nil {
		fmt.Errorf("Error: %s", err)
		return "", err
	}

	vRegexp := regexp.MustCompile(attrribute + "\\s*=\\s*\"([^\"]+)\"")

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if !strings.HasSuffix(file.Name(), ".tf") {
			continue
		}

		filename := fmt.Sprintf("%s/%s", srcDir, file.Name())
		content, err := ioutil.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("Error: %s", err)
		}

		f, diag := hclwrite.ParseConfig(content, file.Name(), hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return "", errors.New(diag.Error())
		}

		for _, block := range f.Body().Blocks() {
			if block.Type() != "terraform" {
				continue
			}

			for _, block := range block.Body().Blocks() {
				if block.Type() != "required_providers" {
					continue
				}

				provMap := block.Body().GetAttribute(provider)
				if provMap == nil {
					continue
				}

				val := provMap.BuildTokens(nil).Bytes()
				constraint := vRegexp.FindSubmatch(val)

				if constraint == nil || len(constraint) < 2 {
					continue
				}

				return string(constraint[1]), nil
			}
		}
	}

	return "", fmt.Errorf("provider %s not found", provider)
}
