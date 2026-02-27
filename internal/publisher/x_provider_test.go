package publisher

import (
	"context"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestXProviderValidateDraftUsesDefaultLimit(t *testing.T) {
	provider := NewXProvider(XConfig{})
	warnings, err := provider.ValidateDraft(context.Background(), domain.SocialAccount{Platform: domain.PlatformX}, Draft{
		Text: strings.Repeat("a", 281),
	})
	if err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "280") {
		t.Fatalf("expected warning to mention 280 chars, got %q", warnings[0])
	}
}

func TestXProviderValidateDraftUsesPremiumLimit(t *testing.T) {
	provider := NewXProvider(XConfig{})
	account := domain.SocialAccount{Platform: domain.PlatformX, XPremium: true}

	warnings, err := provider.ValidateDraft(context.Background(), account, Draft{
		Text: strings.Repeat("a", 300),
	})
	if err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for 300 chars on premium x account, got %v", warnings)
	}

	warnings, err = provider.ValidateDraft(context.Background(), account, Draft{
		Text: strings.Repeat("a", 25001),
	})
	if err != nil {
		t.Fatalf("validate draft with overflow: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for premium overflow, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "25000") {
		t.Fatalf("expected warning to mention 25000 chars, got %q", warnings[0])
	}
}
