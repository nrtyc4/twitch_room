package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// metricFamilyPattern matches the metric names exposed by the service.
var metricFamilyPattern = regexp.MustCompile(`twitch_room_[a-z_]+`)

// knownMetricFamilies is every family the service can emit, so a dashboard or
// alert rule that references anything else is a typo or a rename that was not
// propagated. Keep in sync with handleMetrics, metricsMiddleware and
// incThrottled; adding a family here means adding it to the dashboard too.
var knownMetricFamilies = map[string]bool{
	"twitch_room_http_requests_total":           true,
	"twitch_room_http_request_duration_seconds": true,
	"twitch_room_throttled_requests_total":      true,
	"twitch_room_active_watch_states":           true,
	"twitch_room_subscriptions":                 true,
	"twitch_room_app_info":                      true,
}

// histogramSeriesSuffixes are appended to a family name by a histogram series.
var histogramSeriesSuffixes = []string{"_bucket", "_sum", "_count"}

func baseMetricFamily(name string) string {
	for _, suffix := range histogramSeriesSuffixes {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

// deployArtifact is one parsed file under deploy/.
type deployArtifact struct {
	path string
	body string
}

// loadDeployArtifacts reads every YAML/JSON file under deploy/, validating
// JSON as it goes.
func loadDeployArtifacts(t *testing.T) []deployArtifact {
	t.Helper()

	var artifacts []deployArtifact
	err := filepath.WalkDir("deploy", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yml", ".yaml", ".json":
		default:
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(path), ".json") {
			var anyJSON any
			if err := json.Unmarshal(raw, &anyJSON); err != nil {
				return fmt.Errorf("%s: invalid JSON: %w", path, err)
			}
		}
		artifacts = append(artifacts, deployArtifact{path: path, body: string(raw)})
		return nil
	})
	if err != nil {
		t.Fatalf("reading deploy/: %v", err)
	}
	if len(artifacts) == 0 {
		t.Fatal("no deploy artifacts found under deploy/")
	}
	return artifacts
}

// TestDeployArtifactsReferenceKnownMetrics checks that the Prometheus scrape
// config, the alert rules and the Grafana dashboard only name metric families
// the service really exposes, and cover all of them. A renamed family or a
// typo in a panel otherwise fails silently as an empty panel in production.
func TestDeployArtifactsReferenceKnownMetrics(t *testing.T) {
	artifacts := loadDeployArtifacts(t)

	referenced := map[string]string{} // family -> artifact that mentions it
	for _, artifact := range artifacts {
		for _, loc := range metricFamilyPattern.FindAllStringIndex(artifact.body, -1) {
			name := artifact.body[loc[0]:loc[1]]
			// Skip file names in paths, e.g. /etc/prometheus/twitch_room_alerts.yml.
			if loc[1] < len(artifact.body) && artifact.body[loc[1]] == '.' {
				continue
			}
			family := baseMetricFamily(name)
			referenced[family] = artifact.path
			if !knownMetricFamilies[family] {
				t.Errorf("%s references unknown metric family %q", artifact.path, name)
			}
		}
	}

	// Every family the service emits should be visible somewhere, so a new
	// metric cannot ship without monitoring.
	for family := range knownMetricFamilies {
		if _, ok := referenced[family]; !ok {
			t.Errorf("no deploy artifact references %q; add it to the dashboard (or drop it from knownMetricFamilies)", family)
		}
	}

	// rule_files holds container paths, so the file it names must exist in the
	// directory docker-compose.yml mounts at /etc/prometheus.
	configPath := filepath.Join("deploy", "prometheus", "prometheus.yml")
	for _, artifact := range artifacts {
		if artifact.path != configPath {
			continue
		}
		ruleFilePattern := regexp.MustCompile(`/etc/prometheus/([A-Za-z0-9._-]+\.ya?ml)`)
		matches := ruleFilePattern.FindAllStringSubmatch(artifact.body, -1)
		if len(matches) == 0 {
			t.Errorf("%s loads no rule_files", artifact.path)
		}
		for _, match := range matches {
			if _, err := os.Stat(filepath.Join("deploy", "prometheus", match[1])); err != nil {
				t.Errorf("%s loads %s, but deploy/prometheus/%s does not exist", artifact.path, match[0], match[1])
			}
		}
	}
}

// TestRenderScriptStripsTheCredentialBlock keeps render-config.sh in step with
// prometheus.yml: the script removes the authorization block with grep -v when
// no token is configured (public /metrics), matching on fixed patterns. If the
// block is re-indented or its keys change, those patterns silently stop
// matching and the rendered config keeps a credentials_file that does not
// exist, which Prometheus rejects at startup.
func TestRenderScriptStripsTheCredentialBlock(t *testing.T) {
	configPath := filepath.Join("deploy", "prometheus", "prometheus.yml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading %s: %v", configPath, err)
	}
	scriptPath := filepath.Join("deploy", "prometheus", "render-config.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading %s: %v", scriptPath, err)
	}

	// The lines render-config.sh deletes, paired with the grep pattern it uses
	// for each. The credential path is matched by prefix so the two files
	// cannot drift over the path alone.
	block := []struct {
		configLine  string
		grepPattern string
	}{
		{"    authorization:", "'^    authorization:$'"},
		{"      type: Bearer", "'^      type: Bearer$'"},
		{"      credentials_file: /tmp/prometheus/metrics_token", "'^      credentials_file:'"},
	}
	for _, line := range block {
		if !strings.Contains(string(config), line.configLine+"\n") {
			t.Errorf("%s no longer contains the line %q that render-config.sh strips", configPath, line.configLine)
		}
		if !strings.Contains(string(script), line.grepPattern) {
			t.Errorf("%s does not strip %q with %s; the grep pattern and the config have drifted apart",
				scriptPath, line.configLine, line.grepPattern)
		}
	}
}

// TestDashboardPanelsAreQueryable checks the Grafana dashboard actually parses
// into panels that carry a PromQL expression.
func TestDashboardPanelsAreQueryable(t *testing.T) {
	path := filepath.Join("deploy", "grafana", "twitch-room-dashboard.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var dashboard struct {
		Title  string `json:"title"`
		Panels []struct {
			ID      int    `json:"id"`
			Type    string `json:"type"`
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if dashboard.Title == "" {
		t.Error("dashboard has no title")
	}
	if len(dashboard.Panels) == 0 {
		t.Fatal("dashboard has no panels")
	}

	seenID := map[int]bool{}
	for _, panel := range dashboard.Panels {
		if panel.ID == 0 {
			t.Errorf("panel %q has no id", panel.Title)
		}
		if seenID[panel.ID] {
			t.Errorf("panel id %d is used more than once", panel.ID)
		}
		seenID[panel.ID] = true
		if panel.Title == "" {
			t.Errorf("panel %d has no title", panel.ID)
		}
		if len(panel.Targets) == 0 {
			t.Errorf("panel %q has no targets", panel.Title)
		}
		for _, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				t.Errorf("panel %q has an empty PromQL expression", panel.Title)
			}
		}
	}
}
