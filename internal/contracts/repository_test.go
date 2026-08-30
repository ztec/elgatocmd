// Package contracts keeps public repository identity and policy synchronized.
package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRequiredRepositoryFiles(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{
		".copier-answers.yml", ".forgejo/workflows/renovate.yaml", ".forgejo/workflows/test-and-release.yaml",
		".forgejo/workflows/test.yaml",
		"AGENTS.md", "CONTRIBUTING.md", "Dockerfile", "LICENSE",
		"Makefile", "README.md", "SECURITY.md", "distrobox.ini", "install.sh", "renovate.json",
		"docs/cli.md", "docs/development.md", "docs/releasing.md", "docs/template.md",
		"scripts/package-licenses.sh", "scripts/release-existence-policy.sh", "scripts/renovate-tmplt-ready.sh",
		"scripts/renovate-tmplt-update.sh", "scripts/test-release-existence-policy.sh",
		"scripts/test-renovate-tmplt-ready.sh", "scripts/tmplt-source.sh", "scripts/tmplt-update-validate.sh",
		"tools/copier/Containerfile",
	} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || info.IsDir() {
			t.Errorf("required file %s: %v", name, err)
		}
	}
	for _, forbidden := range []string{
		"CHANGELOG.md", "release-keys", ".forgejo/workflows/release.yaml",
	} {
		if _, err := os.Stat(filepath.Join(root, forbidden)); !os.IsNotExist(err) {
			t.Errorf("generated repository must not contain %s", forbidden)
		}
	}
	updater, err := os.Stat(filepath.Join(root, "scripts/renovate-tmplt-update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if updater.Mode()&0o111 == 0 {
		t.Fatal("Renovate template updater must be executable")
	}
	validator, err := os.Stat(filepath.Join(root, "scripts/tmplt-update-validate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if validator.Mode()&0o111 == 0 {
		t.Fatal("template update validator must be executable")
	}
}

func TestReadmeKeepsDisclaimerInstallAndUpdateNearTheFront(t *testing.T) {
	readme := readFile(t, "README.md")
	disclaimer := "Made by an automated agent. The code was not extensively reviewed;"
	if index := strings.Index(readme, disclaimer); index < 0 || index > 600 {
		t.Fatalf("agent disclaimer is missing or too late (index %d)", index)
	}
	for _, required := range []string{
		"https://git2.riper.fr/ztec/elgatocmd/raw/branch/main/install.sh",
		"https://git2.riper.fr/ztec/tmplt/src/branch/main/release-keys",
		"elgatolight self-update",
		"docs/template.md",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README does not contain %q", required)
		}
	}
	if strings.Contains(readme, "| sh") || !strings.Contains(readme, `-o "$release_installer"`) || !strings.Contains(readme, `sh "$release_installer"`) {
		t.Fatal("README installer command can mask a failed download")
	}
}

func TestTemplateUpdateContract(t *testing.T) {
	guide := readFile(t, "docs/template.md")
	answers := readFile(t, ".copier-answers.yml")
	makefile := readFile(t, "Makefile")
	for _, required := range []string{"make tmplt-check", "make tmplt-update", ".copier-answers.yml", "git2.riper.fr/ztec/tmplt", "INIT"} {
		if !strings.Contains(guide, required) {
			t.Errorf("template guide does not contain %q", required)
		}
	}
	for _, required := range []string{"_commit:", "_src_path:"} {
		if !strings.Contains(answers, required) {
			t.Errorf("answers file does not contain %q", required)
		}
	}
	for _, required := range []string{"tmplt-check:", "tmplt-update:", "template-contract:", "deps-tidy:"} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile does not contain %q", required)
		}
	}
	if strings.Contains(makefile, "signing-key:") {
		t.Fatal("generated Makefile contains the central signing-key target")
	}
	updater := readFile(t, "scripts/renovate-tmplt-update.sh")
	if !strings.Contains(updater, "./scripts/tmplt-update-validate.sh") {
		t.Fatal("Renovate does not use the same template validation contract as manual updates")
	}
}

func TestRecordedTemplateVersionMatchesReusableModule(t *testing.T) {
	answers := readFile(t, ".copier-answers.yml")
	if strings.Contains(answers, "#copier updated") {
		t.Fatal("Copier update marker remains in the answers file")
	}
	version := ""
	for _, line := range strings.Split(answers, "\n") {
		if strings.HasPrefix(line, "_commit:") {
			version = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "_commit:")), "'\"")
		}
	}
	if version == "" {
		t.Fatal("answers file does not record a template version")
	}
	goMod := readFile(t, "go.mod")
	if !strings.Contains(goMod, "git2.riper.fr/ztec/tmplt "+version) {
		t.Fatalf("go.mod does not use recorded template version %s", version)
	}
}

func TestReleaseTrustUsesCentralRegistryWithoutEmbeddingKeys(t *testing.T) {
	buildInfo := readFile(t, "internal/buildinfo/buildinfo.go")
	adapter := readFile(t, "internal/selfupdate/selfupdate.go")
	buildScript := readFile(t, "scripts/build-release.sh")
	installer := readFile(t, "install.sh")
	centralAPI := "https://git2.riper.fr/api/v1/repos/ztec/tmplt"
	centralRepository := "https://git2.riper.fr/ztec/tmplt"
	for name, content := range map[string]string{
		"build info": buildInfo, "release build": buildScript, "installer": installer,
	} {
		if !strings.Contains(content, centralAPI) {
			t.Errorf("%s does not preserve the central signing-key API", name)
		}
	}
	if !strings.Contains(installer, centralRepository) || !strings.Contains(installer, "RELEASE_SIGNING_KEYS_API") {
		t.Fatal("installer does not use the central signing-key registry")
	}
	if !strings.Contains(adapter, "git2.riper.fr/ztec/tmplt/kit/selfupdate") {
		t.Fatal("self-update does not use the reusable Tmplt package")
	}
	if !strings.Contains(buildScript, "git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign") {
		t.Fatal("release build does not use the reusable signing command")
	}
	for _, required := range []string{"--skip-signature-verification", "ELGATOLIGHT_SKIP_SIGNATURE_VERIFICATION", "Checksum verified for"} {
		if !strings.Contains(installer, required) {
			t.Errorf("installer does not contain %q", required)
		}
	}
}

func TestWorkflowsPreferPullRequestsAndConsolidateMainRelease(t *testing.T) {
	testWorkflow := readFile(t, ".forgejo/workflows/test.yaml")
	testAndReleaseWorkflow := readFile(t, ".forgejo/workflows/test-and-release.yaml")
	renovateWorkflow := readFile(t, ".forgejo/workflows/renovate.yaml")
	for _, required := range []string{
		"name: Test", "pull_request:", "workflow_dispatch:", "make ci",
		"cancel-in-progress: true", "https://code.forgejo.org/actions/checkout@",
	} {
		if !strings.Contains(testWorkflow, required) {
			t.Errorf("test workflow does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"push:", "release:", "RELEASE_SIGNING_KEY"} {
		if strings.Contains(testWorkflow, forbidden) {
			t.Errorf("test workflow unexpectedly contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"name: Test and release", "push:", "branches:", "- main", "tags:",
		"release:", "published", "make ci", "needs: test",
		"RELEASE_SIGNING_KEY", "release-notes.sh", "release-existence-policy.sh",
		"expected-release-assets", "actual-release-assets",
	} {
		if !strings.Contains(testAndReleaseWorkflow, required) {
			t.Errorf("test-and-release workflow does not contain %q", required)
		}
	}
	if strings.Contains(testAndReleaseWorkflow, `- "**"`) {
		t.Error("test-and-release workflow still runs automatically for non-main branch pushes")
	}
	for _, forbidden := range []string{"pull_request:", "workflow_dispatch:"} {
		if strings.Contains(testAndReleaseWorkflow, forbidden) {
			t.Errorf("test-and-release workflow unexpectedly contains %q", forbidden)
		}
	}
	if !strings.Contains(testAndReleaseWorkflow, "https://code.forgejo.org/actions/checkout@") {
		t.Error("test-and-release workflow does not use Forgejo's checkout action")
	}
	for _, required := range []string{
		"vars.RENOVATE_ENABLED != 'false'", "RENOVATE_ALLOWED_COMMANDS", "RENOVATE_TOKEN",
		"RENOVATE_GITHUB_COM_TOKEN: ${{ secrets.RENOVATE_GH_TOKEN }}",
		"RENOVATE_DOCKER_MAX_PAGES=10", "RENOVATE_PLATFORM=forgejo",
		"paths:", "- renovate.json", "- .forgejo/workflows/renovate.yaml", "- .copier-answers.yml",
		`cron: "17 5,17 * * *"`, `"includePaths":[".copier-answers.yml"]`,
		`"prHourlyLimit":0`, `"pruneStaleBranches":false`, `"pruneStaleBranches":true`,
		"template_branch=renovate/tmplt-template", "template_pr_open()", "Removing stale Tmplt update branch",
		"--request DELETE", `run_renovate "${template_cleanup_force}"`,
		"pulls?state=open&limit=50&page=${page}", ".head.ref == $branch",
		"Tmplt update PR is open; retiring ordinary dependency PRs until it merges.",
		"renovate-tmplt-ready.sh", "Deferring ordinary dependency proposals", `run_renovate "${full_force}"`,
	} {
		if !strings.Contains(renovateWorkflow, required) {
			t.Errorf("Renovate workflow does not contain %q", required)
		}
	}
}

func TestRenovateUsesForgejoReleaseTimestamps(t *testing.T) {
	config := readFile(t, "renovate.json")
	for _, required := range []string{
		`"packageNameTemplate": "ztec/tmplt"`,
		`"branchTopic": "tmplt-template"`,
		`"commitMessageTopic": "Tmplt template"`,
		`"commitMessageExtra": "{{{prettyNewVersion}}}"`,
		`"prCreation": "immediate"`,
		`"recreateWhen": "always"`,
		`\\.ya?ml(?:\\.jinja)?`,
		`Makefile(?:\\.jinja)?`,
		`"datasourceTemplate": "forgejo-releases"`,
		`"registryUrlTemplate": "https://git2.riper.fr"`,
		`"minimumReleaseAge": "30 days"`,
		`"ignoreUnstable": false`,
		`"minimumReleaseAge": null`,
		`"minimumReleaseAgeBehaviour": "timestamp-optional"`,
		`"postUpgradeTasks"`, "scripts/renovate-tmplt-update.sh",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("Renovate configuration does not contain %q", required)
		}
	}
	if strings.Contains(config, `"datasourceTemplate": "git-tags"`) {
		t.Fatal("Renovate still uses timestamp-less tag discovery for Tmplt")
	}
	if strings.Contains(config, `"schedule":`) {
		t.Fatal("Renovate still limits eligible updates to an internal weekly schedule")
	}
}

func TestRenovateAutomergeAvoidsMergeCommits(t *testing.T) {
	var config struct {
		CommitMessageExtra  string `json:"commitMessageExtra"`
		SemanticCommits     string `json:"semanticCommits"`
		SemanticCommitScope string `json:"semanticCommitScope"`
		PackageRules        []struct {
			Description       string   `json:"description"`
			GroupName         *string  `json:"groupName"`
			MatchFileNames    []string `json:"matchFileNames"`
			MatchUpdateTypes  []string `json:"matchUpdateTypes"`
			Enabled           *bool    `json:"enabled"`
			GroupSingleUpdate *bool    `json:"groupSingleUpdates"`
			Automerge         bool     `json:"automerge"`
			AutomergeType     string   `json:"automergeType"`
			AutomergeStrategy string   `json:"automergeStrategy"`
			PlatformAutomerge *bool    `json:"platformAutomerge"`
			IgnoreTests       *bool    `json:"ignoreTests"`
			RebaseWhen        string   `json:"rebaseWhen"`
		} `json:"packageRules"`
	}
	if err := json.Unmarshal([]byte(readFile(t, "renovate.json")), &config); err != nil {
		t.Fatal(err)
	}
	const versionOnlyMessage = `{{#if isPinDigest}}{{{newDigestShort}}}{{else}}{{#if isMajor}}{{prettyNewMajor}}{{else}}{{#if isSingleVersion}}{{prettyNewVersion}}{{else}}{{#if newValue}}{{{newValue}}}{{else}}{{{newDigestShort}}}{{/if}}{{/if}}{{/if}}{{/if}}`
	if config.CommitMessageExtra != versionOnlyMessage {
		t.Error("Renovate does not append only the target version to external dependency titles")
	}
	if config.SemanticCommits != "enabled" || config.SemanticCommitScope != "deps" {
		t.Error("Renovate does not preserve conventional dependency titles in an INIT-only repository")
	}
	for _, rule := range config.PackageRules {
		if len(rule.MatchFileNames) > 0 && rule.Enabled != nil && !*rule.Enabled {
			t.Errorf("Renovate disables dependency updates by file: %v", rule.MatchFileNames)
		}
		if !strings.Contains(rule.Description, "non-major maintenance") {
			continue
		}
		updates := make(map[string]bool, len(rule.MatchUpdateTypes))
		for _, update := range rule.MatchUpdateTypes {
			updates[update] = true
		}
		for _, expected := range []string{"digest", "pin", "pinDigest", "patch", "minor"} {
			if !updates[expected] {
				t.Errorf("non-major policy does not include %s updates", expected)
			}
		}
		if len(updates) != 5 || updates["major"] {
			t.Errorf("non-major policy can include an unintended update type: %v", rule.MatchUpdateTypes)
		}
		if rule.GroupName != nil || rule.GroupSingleUpdate != nil {
			t.Error("Renovate groups external dependencies, hiding their individual target versions")
		}
		if !rule.Automerge || rule.AutomergeType != "pr" ||
			rule.AutomergeStrategy != "fast-forward" || rule.PlatformAutomerge == nil || *rule.PlatformAutomerge ||
			rule.IgnoreTests == nil || *rule.IgnoreTests || rule.RebaseWhen != "behind-base-branch" {
			t.Error("non-major policy does not require tested, rebased, fast-forward PR automerge")
		}
		return
	}
	t.Error("Renovate does not define the non-major dependency maintenance policy")
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
