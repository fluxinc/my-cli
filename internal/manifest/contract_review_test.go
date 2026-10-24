package manifest

import (
	"strings"
	"testing"
)

func TestContractReviewValidationAndVersionFloor(t *testing.T) {
	doc := Document{ManifestVersion: 1, Organization: Organization{ID: "acme", Name: "Acme"}, ContractReview: ContractReview{Enabled: true, Harness: "claude", Model: "chosen", TimeoutSeconds: 120, MaxBudgetUSD: 1}}
	if floor, _ := FeatureMinMyVersion(doc); floor != "0.50.0" {
		t.Fatal(floor)
	}
	if result := ValidateDocument("", doc); len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	for _, review := range []ContractReview{{Enabled: true}, {Harness: "unknown"}, {TimeoutSeconds: 601}, {MaxBudgetUSD: 11}, {MaxBudgetUSD: -1}} {
		doc.ContractReview = review
		result := ValidateDocument("", doc)
		if !strings.Contains(strings.Join(result.Errors, " "), "contract_review") {
			t.Fatalf("invalid configuration accepted: %+v", review)
		}
	}
}
