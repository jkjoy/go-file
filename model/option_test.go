package model

import (
	"go-file/common"
	"testing"
)

func TestInvalidWebsiteThemeDoesNotChangeSettingsOrReachDatabase(t *testing.T) {
	previousDB, previousOptions := DB, common.OptionMap
	t.Cleanup(func() { DB, common.OptionMap = previousDB, previousOptions })
	DB = nil
	common.OptionMap = map[string]string{"WebsiteTheme": "modern"}
	for _, invalid := range []string{"", "missing", "../modern"} {
		if err := UpdateOption("WebsiteTheme", invalid); err == nil {
			t.Fatalf("theme %q must be rejected", invalid)
		}
		if got := common.OptionMap["WebsiteTheme"]; got != "modern" {
			t.Fatalf("invalid theme changed the current setting to %q", got)
		}
	}
}

func TestStoredWebsiteThemeFallsBackWhenTemplateIsUnavailable(t *testing.T) {
	previousOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = previousOptions })
	common.OptionMap = make(map[string]string)
	updateOptionMap("WebsiteTheme", "removed-theme")
	if got := common.OptionMap["WebsiteTheme"]; got != common.DefaultThemeID {
		t.Fatalf("unavailable theme defaulted to %q, want %q", got, common.DefaultThemeID)
	}
	updateOptionMap("WebsiteTheme", "modern")
	if got := common.OptionMap["WebsiteTheme"]; got != "modern" {
		t.Fatalf("installed theme changed to %q", got)
	}
}
