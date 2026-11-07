package validator

// NotebookServerTypeOrDefault 仅为未指定类型的历史请求/命令兼容 Jupyter。
// 不推断镜像名称；未知的非空类型仍必须显式拒绝。
func NotebookServerTypeOrDefault(value string) string {
	if value == "" {
		return "jupyter"
	}
	return value
}

func IsNotebookServerType(value string) bool {
	return value == "jupyter" || value == "group-one" || value == "group-two"
}

func IsNotebookImageCategory(value string) bool {
	return value == "jupyter" || value == "vscode" || value == "rstudio"
}
