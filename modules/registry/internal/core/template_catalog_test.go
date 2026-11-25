package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDeviceTemplateCatalogEntriesSatisfyRegistryReleaseValidation(t *testing.T) {
	catalogDir := filepath.Join("..", "..", "..", "..", "deploy", "templates", "devices")
	entries, err := os.ReadDir(catalogDir)
	if err != nil {
		t.Fatalf("read catalog dir %s: %v", catalogDir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) < 6 {
		t.Fatalf("expected at least 6 standard device templates, got %d", len(names))
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(catalogDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc struct {
			TemplateKey       string            `json:"templateKey"`
			TemplateKind      TemplateKind      `json:"templateKind"`
			Payload           map[string]any    `json:"payload"`
			ReleaseReferences map[string]string `json:"releaseReferences"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		req := ReleaseTemplateRequest{
			TemplateKey:       doc.TemplateKey,
			TemplateKind:      doc.TemplateKind,
			Payload:           doc.Payload,
			ReleaseReferences: doc.ReleaseReferences,
			Meta: MutationMeta{
				IdempotencyKey: "catalog-test:" + doc.TemplateKey,
				Reason:         "validate standard HVAC device template catalog entry",
			},
		}
		if err := req.Validate(); err != nil {
			t.Fatalf("template %s (%s) failed ReleaseTemplateRequest.Validate(): %v", name, doc.TemplateKey, err)
		}
	}
}
