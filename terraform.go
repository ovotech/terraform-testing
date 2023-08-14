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
	"runtime"
	"strings"
	"testing"
	"time"

	version "github.com/hashicorp/go-version"
	hcl "github.com/hashicorp/hcl/v2"
	hclwrite "github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GetAvailableVersionsE returns all of the versions available for the
// given provider or the Terraform binary, or returns an error if something goes wrong
func GetAvailableVersionsE(release string) ([]string, error) {
	var versions []string

	client := http.Client{Timeout: 5 * time.Second}
	req := fmt.Sprintf("https://api.releases.hashicorp.com/v1/releases/%s?limit=20", release)

	for {
		resp, err := client.Get(req)
		if err != nil {
			return nil, err
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}

		var result []struct {
			Version   string `json:"version"`
			CreatedAt string `json:"timestamp_created"`
		}

		if err := json.Unmarshal(body, &result); err != nil {
			return nil, err
		}

		if len(result) == 0 {
			break
		}

		for _, res := range result {
			versions = append(versions, res.Version)
			req = fmt.Sprintf("https://api.releases.hashicorp.com/v1/releases/%s?limit=20&after=%s", release, res.CreatedAt)
		}
	}

	return versions, nil
}

// GetAvailableVersions returns all the released versions of a provider or the Terraform binary
// or fails the test if something goes wrong
func GetAvailableVersions(t *testing.T, release string) []string {
	out, err := GetAvailableVersionsE(release)
	if err != nil {
		t.Fatalf(err.Error())
	}
	return out
}

// GetMatchingVersionsE returns a slice of the matching version strings that meet the
// constraint criteria given, or an error if something goes wrong
func GetMatchingVersionsE(constraint string, versions []string) ([]string, error) {
	want, err := version.NewConstraint(constraint)
	if err != nil {
		return nil, err
	}

	var vers []*version.Version
	for _, ver := range versions {
		vObj, err := version.NewVersion(ver)
		if err != nil {
			return nil, err
		}

		vers = append(vers, vObj)
	}

	var matching []string
	for _, ver := range vers {
		if want.Check(ver) {
			matching = append(matching, ver.String())
		}
	}

	return matching, nil
}

// GetMatchingVersions returns a slice of all the given versions that meet the given constraint
// or fails the test if something goes wrong
func GetMatchingVersions(t *testing.T, constraint string, versions []string) []string {
	out, err := GetMatchingVersionsE(constraint, versions)
	if err != nil {
		t.Fatalf(err.Error())
	}
	return out
}

// UpdateModuleSourceAndVersionE will update the specified modules source and version with
// the given values.
//
// Usage:
//   - srcDir is the directory that contains the Terraform source files to update.
//   - module is the name of the module block to update. Use "*" as a wildcard to update all
//     modules
//   - src is the new source to update the module to
//   - ver is the new version to update the module to. Use "" to remove the version string.
func UpdateModuleSourceAndVersionE(srcDir, module, src, ver string) error {
	if src == ".." {
		src = "../"
	}

	return IterateTerraformInDirectory(srcDir, func(filename string, f *hclwrite.File) error {
		hasChanges := false
		for _, block := range f.Body().Blocks() {
			if block.Type() != "module" {
				continue
			}

			if len(block.Labels()) != 1 {
				continue
			}

			if block.Labels()[0] != module && module != "*" {
				continue
			}

			block.Body().SetAttributeValue("source", cty.StringVal(src))
			if ver != "" {
				block.Body().SetAttributeValue("version", cty.StringVal(ver))
			} else {
				block.Body().RemoveAttribute("version")
			}

			hasChanges = true
		}

		if hasChanges {
			if err := os.WriteFile(filename, f.Bytes(), 0666); err != nil {
				return err
			}
		}

		return nil
	})
}

// IterateTerraformInDirectory will iterate over the files in a directory, running the
// callback function for every Terraform file found.
func IterateTerraformInDirectory(dir string, fn func(filename string, f *hclwrite.File) error) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if !strings.HasSuffix(file.Name(), ".tf") {
			continue
		}

		filename := fmt.Sprintf("%s/%s", dir, file.Name())
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}

		f, diag := hclwrite.ParseConfig(content, file.Name(), hcl.Pos{Line: 1, Column: 1})
		if diag.HasErrors() {
			return diag
		}

		if err = fn(filename, f); err != nil {
			return err
		}
	}

	return nil
}

// UpdateModuleSourceAndVersion will update the specified modules source and version with
// the given values.
//
// Usage:
//   - t is the test runner instance.
//   - srcDir is the directory that contains the Terraform source files to update.
//   - module is the name of the module block to update. Use "*" as a wildcard to update all
//     modules
//   - src is the new source to update the module to
//   - ver is the new version to update the module to. Use "" to remove the version string.
func UpdateModuleSourceAndVersion(t *testing.T, srcDir, module, src, ver string) {
	err := UpdateModuleSourceAndVersionE(srcDir, module, src, ver)
	if err != nil {
		t.Fatalf("error when attempting to update the module source: %s", err)
	}
}

// UpdateModuleSourceToPathE will update the specified modules source to the given path
// to enable testing examples by changing the repository location from the remote source
// to the given local one with changes. It will return an error if unable to do so.
//
// Usage:
//   - srcDir is the directory that contains the Terraform source files to update.
//   - module is the name of the module block to update. Use "*" as a wildcard to update all
//     modules
//   - path is the relative path to update the module source argument to
func UpdateModuleSourceToPathE(srcDir, module, path string) error {
	return UpdateModuleSourceAndVersionE(srcDir, module, path, "")
}

// UpdateModuleSourceToPath will update the specified modules source to the given path
// to enable testing examples by changing the repository location from the remote source
// to the given local one with changes.
//
// Usage:
//   - t is the test runner instance.
//   - srcDir is the directory that contains the Terraform source files to update.
//   - module is the name of the module block to update. Use "*" as a wildcard to update all
//     modules
//   - path is the relative path to update the module source argument to
func UpdateModuleSourceToPath(t *testing.T, srcDir, module, path string) {
	err := UpdateModuleSourceAndVersionE(srcDir, module, path, "")
	if err != nil {
		t.Fatalf("error when attempting to update the module source: %s", err)
	}
}

// UpdateModuleSourceToAbsolutePath will update the specified modules source to the given path
// by turning it into an absolute path to enable testing examples by changing the repository
// location from the remote source to the given local one with changes.
//
// Usage:
//   - t is the test runner instance.
//   - srcDir is the directory that contains the Terraform source files to update.
//   - module is the name of the module block to update. Use "*" as a wildcard to update all
//     modules
//   - path is the relative path to update the module source argument to
func UpdateModuleSourceToAbsolutePath(t *testing.T, srcDir, module, path string) {
	absModulePath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("error when attempting to get the absolute path: %s", err)
	}
	UpdateModuleSourceToPath(t, srcDir, module, absModulePath)
}

// UpdateProviderVersionE will update the specified provider's version with the given value.
//
// Usage:
// * dir is the directory that contains the Terraform source files to update.
// * provider is the name of the provider to update.
// * version is the new version to update the provider to.
// * providerSource is the source of the provider being tested.
func UpdateProviderVersionE(dir, provider, version string, providerSource string) error {
	return IterateTerraformInDirectory(dir, func(filename string, f *hclwrite.File) error {
		hasChanges := false
		for _, block := range f.Body().Blocks() {
			if block.Type() != "terraform" {
				continue
			}

			for _, block := range block.Body().Blocks() {
				if block.Type() != "required_providers" {
					continue
				}

				block.Body().SetAttributeValue(provider, cty.MapVal(map[string]cty.Value{
					"version": cty.StringVal(version),
					"source":  cty.StringVal(providerSource),
				}))

				hasChanges = true
			}
		}

		if hasChanges {
			if err := ioutil.WriteFile(filename, f.Bytes(), 0666); err != nil {
				return err
			}

			lockfile := fmt.Sprintf("%s/.terraform.lock.hcl", dir)
			_ = os.Remove(lockfile)
		}

		return nil
	})
}

// UpdateProviderVersion will update the specified provider's version with the given value.
//
// Usage:
// * dir is the directory that contains the Terraform source files to update.
// * provider is the name of the provider to update.
// * version is the new version to update the provider to.
// * providerSource is the source of the provider being tested.
func UpdateProviderVersion(t *testing.T, dir, provider, version string, providerSource string) {
	err := UpdateProviderVersionE(dir, provider, version, providerSource)
	if err != nil {
		t.Fatalf(err.Error())
	}
}

// GetTerraformBinaryUrlE will return the correct download URL for the terraform binary version requested
// based on the operating system and architecture by fetching it from the Hashicorp releases API
//
// Usage:
// * version is the version of Terraform to download.
func GetTerraformBinaryUrlE(version string) (string, error) {

	var binaryUrl string
	operatingSystem := runtime.GOOS
	architecture := runtime.GOARCH

	releasesApiReq := fmt.Sprintf("https://api.releases.hashicorp.com/v1/releases/terraform/%s", version)
	resp, err := http.Get(releasesApiReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", err
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
		return "", errors.New("Unable to find an appropriate Terraform binary download URL for the underlying OS and architecture")
	}

}

// Closure to address file descriptors issue with all the deferred .Close() methods
// Sample code taken from https://stackoverflow.com/questions/20357223/easy-way-to-unzip-file-with-golang
func extractAndWriteFile(dst string, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() {
		if err := rc.Close(); err != nil {
			panic(err)
		}
	}()

	path := filepath.Join(dst, f.Name)

	// Check for ZipSlip (Directory traversal)
	if !strings.HasPrefix(path, filepath.Clean(dst)+string(os.PathSeparator)) {
		return fmt.Errorf("Error: illegal file path: %s", path)
	}

	if f.FileInfo().IsDir() {
		os.MkdirAll(path, f.Mode())
	} else {
		os.MkdirAll(filepath.Dir(path), f.Mode())
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		defer func() {
			if err := f.Close(); err != nil {
				panic(err)
			}
		}()

		_, err = io.Copy(f, rc)
		if err != nil {
			return err
		}
	}
	return nil
}

// DownloadTerraformVersionE will download the specified version of Terraform into the ~/.terraform.versions directory
//
// Usage:
// * version is the version of Terraform to download.
func DownloadTerraformVersionE(version string) (binaryPath string, err error) {

	// Initialise all path variables
	homeDirectory, _ := os.UserHomeDir()
	binaryDownloadDirectory := filepath.Join(homeDirectory, ".terraform.versions")
	binaryPath = binaryDownloadDirectory + "/terraform_" + version

	// Don't do anything if the required binary already exists
	if _, err := os.Stat(binaryPath); errors.Is(err, os.ErrNotExist) {

		// Create ~/.terraform.versions directory if it doesn't exist
		// https://gist.github.com/ivanzoid/5040166bb3f0c82575b52c2ca5f5a60c
		if _, err := os.Stat(binaryDownloadDirectory); os.IsNotExist(err) {
			os.Mkdir(binaryDownloadDirectory, os.ModeDir|0755)
		}

		var binaryUrl string
		binaryUrl, err := GetTerraformBinaryUrlE(version)
		if err != nil {
			return "", err
		}

		req := fmt.Sprintf(binaryUrl)

		resp, err := http.Get(req)
		if err != nil {
			return "", err
		}

		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Errorf("Error: non 200 response code")
			return "", nil
		}

		// Create the file
		out, err := os.Create(binaryDownloadDirectory + "/" + "terraform_" + version + "_binary.zip")
		if err != nil {
			fmt.Errorf("Error: %s", err)
		}

		// Write the body to file
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			fmt.Errorf("Error: %s", err)
		}

		// Sample code to extract zip file taken from https://stackoverflow.com/questions/20357223/easy-way-to-unzip-file-with-golang
		r, err := zip.OpenReader(out.Name())
		if err != nil {
			return "", err
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
				return "", err
			}
		}

		// Move file from temporarily extracted location
		oldBinaryLocation := zipExtractPath + "/terraform"
		err = os.Rename(oldBinaryLocation, binaryPath)
		if err != nil {
			return "", err
		}
	}

	return binaryPath, nil
}

// DownloadTerraformVersion will download the specified version of Terraform into the ~/.terraform.versions directory.
//
// Usage:
// * version is the version of Terraform to download.
func DownloadTerraformVersion(t *testing.T, version string) string {
	binaryPath, err := DownloadTerraformVersionE(version)
	if err != nil {
		t.Fatalf(err.Error())
	}

	return binaryPath
}
