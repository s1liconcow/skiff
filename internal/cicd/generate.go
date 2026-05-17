package cicd

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	TargetGitHubActions = "github-actions"
	TargetGitLab        = "gitlab"
	TargetBuildkite     = "buildkite"
)

var targets = map[string]string{
	TargetGitHubActions: "templates/github_actions.yaml.tmpl",
	TargetGitLab:        "templates/gitlab.yml.tmpl",
	TargetBuildkite:     "templates/buildkite.yml.tmpl",
}

var fileNames = map[string]string{
	TargetGitHubActions: "skiff-github-actions.yml",
	TargetGitLab:        ".gitlab-ci.yml",
	TargetBuildkite:     "pipeline.yml",
}

var requiredCommands = []string{
	"skiff validate",
	"skiff contract test",
	"skiff plan",
	"skiff release candidate create",
	"skiff deploy",
	"skiff promote",
}

//go:embed templates/*.tmpl
var templateFS embed.FS

type Options struct {
	Service         string
	SpecPath        string
	StateURI        string
	Provider        string
	Region          string
	StagingEnv      string
	ProductionEnv   string
	ImageRepository string
	InstallCommand  string
	SkiffBinary     string
}

type Template struct {
	Target   string   `json:"target"`
	FileName string   `json:"file_name"`
	Content  string   `json:"content"`
	Commands []string `json:"commands"`
}

func Targets() []string {
	return []string{TargetGitHubActions, TargetGitLab, TargetBuildkite}
}

func Generate(target string, opts Options) (*Template, error) {
	target = normalizeTarget(target)
	name, ok := targets[target]
	if !ok {
		return nil, fmt.Errorf("unsupported CI target %q; expected github-actions, gitlab, or buildkite", target)
	}
	opts = normalizeOptions(opts)
	body, err := templateFS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New(filepath.Base(name)).Delims("[[", "]]").Option("missingkey=error").Parse(string(body))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, opts); err != nil {
		return nil, err
	}
	return &Template{
		Target:   target,
		FileName: fileNames[target],
		Content:  out.String(),
		Commands: append([]string(nil), requiredCommands...),
	}, nil
}

func normalizeOptions(opts Options) Options {
	opts.Service = firstNonEmpty(opts.Service, "payments-api")
	opts.SpecPath = firstNonEmpty(opts.SpecPath, "skiff.yaml")
	opts.StateURI = firstNonEmpty(opts.StateURI, "s3://skiff-state-prod")
	opts.Provider = firstNonEmpty(opts.Provider, "aws")
	opts.Region = firstNonEmpty(opts.Region, "us-west-2")
	opts.StagingEnv = firstNonEmpty(opts.StagingEnv, "staging")
	opts.ProductionEnv = firstNonEmpty(opts.ProductionEnv, "prod")
	opts.ImageRepository = firstNonEmpty(opts.ImageRepository, "registry.example.com/"+opts.Service)
	opts.InstallCommand = firstNonEmpty(opts.InstallCommand, "curl -fsSL https://get.skiff.dev | sh")
	opts.SkiffBinary = firstNonEmpty(opts.SkiffBinary, "skiff")
	return opts
}

func normalizeTarget(target string) string {
	return strings.TrimSpace(strings.ToLower(target))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
