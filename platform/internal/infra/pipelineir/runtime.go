package pipelineir

import _ "embed"

//go:embed runtime.py
var PythonLauncher string

//go:embed runtime_official.py
var officialPythonLauncher string

//go:embed upstream/output_parameters.py
var officialOutputParameters string

//go:embed runtime_v3.py
var advancedPythonLauncher string

//go:embed artifact_archive.py
var artifactArchive string

// LauncherForCompiler keeps already frozen attempts on their original launcher.
func LauncherForCompiler(version string) string {
	if version == AdvancedCompilerVersion {
		return officialOutputParameters + "\n" + artifactArchive + "\n" + advancedPythonLauncher
	}
	if version == OfficialCompilerVersion {
		return officialOutputParameters + "\n" + officialPythonLauncher
	}
	return PythonLauncher
}
