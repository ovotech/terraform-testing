package testhelpers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/gruntwork-io/terratest/modules/files"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/mattn/go-zglob"
	"github.com/zclconf/go-cty/cty"
)

func UpdateModuleSourcesToLocalPaths(t *testing.T, dst string) {
	metadata, err := GetModuleMetadataCatalog()
	if err != nil {
		t.Fatalf("Error when building the module metadata catalog: %s", err.Error())
	}

	err = IterateTerraformInDirectory(dst, func(filename string, f *hclwrite.File) error {
		hasChanges := false

		for _, block := range f.Body().Blocks() {
			if block.Type() != "module" || len(block.Labels()) != 1 {
				continue
			}

			source := block.Body().GetAttribute("source").Expr().BuildTokens(nil).Bytes()
			path, ok := metadata.Resolve(string(source))
			if !ok {
				continue
			}

			target, err := files.CopyTerraformFolderToTemp(path, cleanName(t.Name()))
			if err != nil {
				return err
			}

			UpdateModuleSourcesToLocalPaths(t, target)
			block.Body().SetAttributeValue("source", cty.StringVal(target))
			block.Body().RemoveAttribute("version")

			hasChanges = true
		}

		if hasChanges {
			if err := os.WriteFile(filename, f.Bytes(), 0666); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("An error occurred when attempting to resolve all module sources to local paths: %s", err.Error())
	}
}

type ModuleMetadata struct {
	Organisation string
	Name         string
	Provider     string
	LocalPath    string
}

type ModuleMetadataCatalog struct {
	Meta []ModuleMetadata
	mx   sync.RWMutex

	init bool
	root string
}

var mmc = initModuleMetadataCatalog()

func (mmc *ModuleMetadataCatalog) Resolve(src string) (string, bool) {
	parts := strings.Split(strings.Trim(src, " \""), "/")
	if len(parts) != 4 {
		return "", false
	}

	mmc.mx.RLock()
	defer mmc.mx.RUnlock()

	for _, meta := range mmc.Meta {
		if meta.Organisation == parts[1] && meta.Name == parts[2] && meta.Provider == parts[3] {
			return meta.LocalPath, true
		}
	}

	return "", false
}

func (mmc *ModuleMetadataCatalog) Init() error {
	mmc.mx.Lock()
	defer mmc.mx.Unlock()

	pattern := fmt.Sprintf("%s/**/metadata.json", mmc.root)
	matches, err := zglob.Glob(pattern)
	if err != nil {
		return err
	}

	mmc.Meta = make([]ModuleMetadata, 0)

	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		metadata := struct {
			Publish struct {
				Name         string `json:"name"`
				Provider     string `json:"provider"`
				Organisation string `json:"organisation"`
			} `json:"publish"`
		}{}
		err = json.Unmarshal(content, &metadata)
		if err != nil {
			return err
		}

		parts := strings.Split(path, string(os.PathSeparator))
		dir := strings.Join(parts[:len(parts)-1], string(os.PathSeparator))

		module := ModuleMetadata{
			Organisation: metadata.Publish.Organisation,
			Name:         metadata.Publish.Name,
			Provider:     metadata.Publish.Provider,
			LocalPath:    dir,
		}

		if module.Organisation == "" {
			module.Organisation = "ovotech"
		}

		mmc.Meta = append(mmc.Meta, module)
	}

	mmc.init = true
	return nil
}

func (mmc *ModuleMetadataCatalog) SetRoot(root string) {
	mmc.root = root
	mmc.init = false
}

func GetModuleMetadataCatalog() (*ModuleMetadataCatalog, error) {
	if !mmc.init {
		if err := mmc.Init(); err != nil {
			return nil, err
		}
	}

	return mmc, nil
}

func initModuleMetadataCatalog() *ModuleMetadataCatalog {
	mmc := ModuleMetadataCatalog{}

	path, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return &mmc
	}

	mmc.root = strings.TrimSpace(string(path))
	mmc.init = false

	return &mmc
}

func cleanName(originalName string) string {
	parts := strings.Split(originalName, "/")
	return parts[len(parts)-1]
}
