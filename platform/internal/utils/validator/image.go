package validator

import "regexp"

// 只校验受支持的容器引用语法，不证明仓库可达、镜像可信或运行兼容。
// digest 当前仅接受 sha256；不允许 URL scheme、用户凭据或空白。
var notebookImageReference = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?(?::[0-9]+)?/)?[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)*(?::[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?(?:@sha256:[a-fA-F0-9]{64})?$`)

func IsNotebookImageReference(value string) bool {
	return IsContainerImageReference(value)
}

func IsContainerImageReference(value string) bool {
	return len(value) > 0 && len(value) <= 512 && notebookImageReference.MatchString(value)
}
