package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestParseGitHubWebhook_DeploymentStatus(t *testing.T) {
	payload := `{
		"deployment": {
			"id": 12345,
			"sha": "a1b2c3d4e5f6",
			"ref": "main",
			"environment": "canary",
			"description": "Deploy canary v2.1.0 with pooled DB connections",
			"creator": {
				"login": "octocat"
			}
		},
		"deployment_status": {
			"state": "success",
			"description": "Deployment finished"
		},
		"repository": {
			"name": "payment-worker"
		}
	}`

	secret := "test-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	event, err := ParseGitHubWebhook([]byte(payload), "deployment_status", sig, secret)
	if err != nil {
		t.Fatalf("ParseGitHubWebhook failed: %v", err)
	}

	if event.ServiceName != "payment-worker" {
		t.Errorf("expected service name payment-worker, got %s", event.ServiceName)
	}
	if event.CommitHash != "a1b2c3d4e5f6" {
		t.Errorf("expected commit hash a1b2c3d4e5f6, got %s", event.CommitHash)
	}
	if event.Environment != "canary" {
		t.Errorf("expected canary environment, got %s", event.Environment)
	}
	if event.Author != "octocat" {
		t.Errorf("expected author octocat, got %s", event.Author)
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	payload := []byte(`{"hello":"world"}`)
	invalidSig := "sha256=invalidhex"
	if VerifySignature(payload, invalidSig, "secret") {
		t.Errorf("expected invalid signature to fail")
	}
}
