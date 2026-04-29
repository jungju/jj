package serve

import (
	"strings"
	"testing"
)

func TestRecentRunItemFromDashboardNormalizesSafePresentationStates(t *testing.T) {
	secret := "sk-proj-statusdto1234567890"
	tests := []struct {
		name string
		item recentRunItem
		want RecentRunItem
		ok   bool
	}{
		{
			name: "available",
			item: recentRunItem{
				State:            "available",
				RunID:            "20260429-120000-good",
				Status:           "complete",
				ProviderOrResult: "codex",
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "2026-04-29T12:00:00Z",
			},
			want: RecentRunItem{
				State:            "available",
				RunID:            "20260429-120000-good",
				Status:           "complete",
				ProviderOrResult: "codex",
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "2026-04-29T12:00:00Z",
			},
			ok: true,
		},
		{
			name: "denied",
			item: recentRunItem{
				State:            "denied",
				RunID:            "20260429-121000-denied",
				Status:           "complete",
				ProviderOrResult: "codex",
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "2026-04-29T12:10:00Z",
			},
			want: RecentRunItem{
				State:            "denied",
				RunID:            "20260429-121000-denied",
				Status:           "denied",
				ProviderOrResult: "denied",
				EvaluationState:  "denied",
				ValidationState:  "denied",
				TimestampLabel:   "2026-04-29T12:10:00Z",
			},
			ok: true,
		},
		{
			name: "unavailable preserves explicit none states",
			item: recentRunItem{
				State:            "unavailable",
				RunID:            "20260429-122000-unavailable",
				Status:           "failed",
				ProviderOrResult: "local",
				EvaluationState:  "none",
				ValidationState:  "none",
			},
			want: RecentRunItem{
				State:            "unavailable",
				RunID:            "20260429-122000-unavailable",
				Status:           "unavailable",
				ProviderOrResult: "unavailable",
				EvaluationState:  "none",
				ValidationState:  "none",
				TimestampLabel:   "unknown",
			},
			ok: true,
		},
		{
			name: "unknown",
			item: recentRunItem{
				State:            "unknown",
				RunID:            "20260429-123000-unknown",
				Status:           "complete",
				ProviderOrResult: "codex",
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "not-a-time",
			},
			want: RecentRunItem{
				State:            "unknown",
				RunID:            "20260429-123000-unknown",
				Status:           "unknown",
				ProviderOrResult: "unknown",
				EvaluationState:  "unknown",
				ValidationState:  "unknown",
				TimestampLabel:   "unknown",
			},
			ok: true,
		},
		{
			name: "none",
			item: recentRunItem{
				State:           "none",
				RunID:           "20260429-124000-none",
				EvaluationState: "none",
				ValidationState: "none",
				TimestampLabel:  "none",
			},
			want: RecentRunItem{
				State:            "none",
				RunID:            "20260429-124000-none",
				Status:           "unknown",
				ProviderOrResult: "unknown",
				EvaluationState:  "none",
				ValidationState:  "none",
				TimestampLabel:   "none",
			},
			ok: true,
		},
		{
			name: "stale status",
			item: recentRunItem{
				State:            "available",
				RunID:            "20260429-125000-stale",
				Status:           "stale",
				ProviderOrResult: "result stale",
				EvaluationState:  "stale",
				ValidationState:  "stale",
				TimestampLabel:   "2026-04-29T12:50:00Z",
			},
			want: RecentRunItem{
				State:            "unavailable",
				RunID:            "20260429-125000-stale",
				Status:           "unavailable",
				ProviderOrResult: "unavailable",
				EvaluationState:  "unavailable",
				ValidationState:  "unavailable",
				TimestampLabel:   "2026-04-29T12:50:00Z",
			},
			ok: true,
		},
		{
			name: "malformed status",
			item: recentRunItem{
				State:          "available",
				RunID:          "20260429-126000-malformed",
				Status:         "malformed",
				TimestampLabel: "2026-04-29T13:00:00Z",
			},
			want: RecentRunItem{
				State:            "unavailable",
				RunID:            "20260429-126000-malformed",
				Status:           "unavailable",
				ProviderOrResult: "unavailable",
				EvaluationState:  "unavailable",
				ValidationState:  "unavailable",
				TimestampLabel:   "2026-04-29T13:00:00Z",
			},
			ok: true,
		},
		{
			name: "partial status",
			item: recentRunItem{
				State:          "available",
				RunID:          "20260429-127000-partial",
				Status:         "partial",
				TimestampLabel: "2026-04-29T13:10:00Z",
			},
			want: RecentRunItem{
				State:            "unavailable",
				RunID:            "20260429-127000-partial",
				Status:           "unavailable",
				ProviderOrResult: "unavailable",
				EvaluationState:  "unavailable",
				ValidationState:  "unavailable",
				TimestampLabel:   "2026-04-29T13:10:00Z",
			},
			ok: true,
		},
		{
			name: "inconsistent status",
			item: recentRunItem{
				State:          "available",
				RunID:          "20260429-128000-inconsistent",
				Status:         "inconsistent",
				TimestampLabel: "2026-04-29T13:20:00Z",
			},
			want: RecentRunItem{
				State:            "unknown",
				RunID:            "20260429-128000-inconsistent",
				Status:           "unknown",
				ProviderOrResult: "unknown",
				EvaluationState:  "unknown",
				ValidationState:  "unknown",
				TimestampLabel:   "2026-04-29T13:20:00Z",
			},
			ok: true,
		},
		{
			name: "hostile provider label",
			item: recentRunItem{
				State:            "available",
				RunID:            "20260429-129000-hostile",
				Status:           "complete",
				ProviderOrResult: "Authorization: Bearer " + secret,
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "2026-04-29T13:30:00Z",
			},
			want: RecentRunItem{
				State:            "available",
				RunID:            "20260429-129000-hostile",
				Status:           "complete",
				ProviderOrResult: "unknown",
				EvaluationState:  "passed",
				ValidationState:  "passed",
				TimestampLabel:   "2026-04-29T13:30:00Z",
			},
			ok: true,
		},
		{
			name: "token like run id",
			item: recentRunItem{
				State: "available",
				RunID: secret,
			},
			ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := recentRunItemFromDashboard(tt.item)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v; item = %#v", ok, tt.ok, got)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Fatalf("recent run item changed:\nwant %#v\ngot  %#v", tt.want, got)
			}
			gotText := strings.Join([]string{
				got.State,
				got.RunID,
				got.Status,
				got.ProviderOrResult,
				got.EvaluationState,
				got.ValidationState,
				got.TimestampLabel,
			}, " ")
			if strings.Contains(gotText, secret) {
				t.Fatalf("recent run item leaked token-like value: %#v", got)
			}
		})
	}
}
