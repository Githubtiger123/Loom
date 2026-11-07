package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gopkg.in/yaml.v3"
)

// NotebookImage 是可信配置，不接受业务请求直接提交地址或修改目录。
type NotebookImage struct {
	ID         string `yaml:"id"`
	Category   string `yaml:"category"`
	Name       string `yaml:"name"`
	ServerType string `yaml:"serverType"`
	Image      string `yaml:"image"`
	Enabled    bool   `yaml:"enabled"`
}

// NotebookImageCatalog 启动时加载，之后只读；Lookup 返回值副本。
type NotebookImageCatalog struct {
	byID    map[string]NotebookImage
	entries []NotebookImage
}

func (c *NotebookImageCatalog) Lookup(id string) (NotebookImage, bool) {
	if c == nil {
		return NotebookImage{}, false
	}
	entry, ok := c.byID[id]
	return entry, ok
}

// Entries 返回配置顺序的副本，调用方不能修改启动时加载的目录。
func (c *NotebookImageCatalog) Entries() []NotebookImage {
	if c == nil {
		return []NotebookImage{}
	}
	return append([]NotebookImage{}, c.entries...)
}

// LoadNotebookImages 的相对路径基于进程工作目录，与其他文件配置保持一致。
// 不做热更新、不访问镜像仓库，也不要求运维/观测命令加载此文件。
func LoadNotebookImages(path string) (*NotebookImageCatalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Notebook 镜像配置 %s: %w", path, err)
	}
	defer f.Close()
	var document struct {
		Images []NotebookImage `yaml:"images"`
	}
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("解析 Notebook 镜像配置 %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("Notebook 镜像配置只能包含一个 YAML 文档")
	}
	if len(document.Images) == 0 {
		return nil, fmt.Errorf("Notebook 镜像配置 images 不能为空；可用 enabled=false 禁用新建")
	}
	catalog := &NotebookImageCatalog{byID: make(map[string]NotebookImage, len(document.Images))}
	serverTypes := map[string]string{"jupyter": "jupyter", "vscode": "group-one", "rstudio": "group-two"}
	for i, entry := range document.Images {
		if !validator.IsCatalogID(entry.ID) || strings.TrimSpace(entry.ID) != entry.ID {
			return nil, fmt.Errorf("Notebook 镜像配置第 %d 项 id 无效", i+1)
		}
		if _, exists := catalog.byID[entry.ID]; exists {
			return nil, fmt.Errorf("Notebook 镜像配置 id %q 重复", entry.ID)
		}
		if strings.TrimSpace(entry.Name) == "" || !validator.MaxTextLength(entry.Name, 128) {
			return nil, fmt.Errorf("Notebook 镜像 %q name 必须为 1–128 字", entry.ID)
		}
		if !validator.IsNotebookServerType(entry.ServerType) || serverTypes[entry.Category] != entry.ServerType {
			return nil, fmt.Errorf("Notebook 镜像 %q category/serverType 必须为 jupyter/jupyter、vscode/group-one 或 rstudio/group-two", entry.ID)
		}
		if !validator.IsNotebookImageReference(entry.Image) {
			return nil, fmt.Errorf("Notebook 镜像 %q image 地址格式无效", entry.ID)
		}
		catalog.byID[entry.ID] = entry
		catalog.entries = append(catalog.entries, entry)
	}
	return catalog, nil
}
