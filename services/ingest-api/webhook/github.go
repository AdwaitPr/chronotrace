package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

// VerifySignature validates a GitHub HMAC-SHA256 webhook payload signature.
func VerifySignature(payload []byte, signatureHeader string, secret string) bool {
	if secret == "" {
		return true // Optional HMAC verification if secret is not set
	}
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	sigHex := strings.TrimPrefix(signatureHeader, "sha256=")
	expectedMAC, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	actualMAC := mac.Sum(nil)

	return hmac.Equal(actualMAC, expectedMAC)
}

// ParseGitHubWebhook parses GitHub deployment, push, or workflow_run events into a DeployEvent.
func ParseGitHubWebhook(payload []byte, eventType, signature, secret string) (*types.DeployEvent, error) {
	if secret != "" && !VerifySignature(payload, signature, secret) {
		return nil, fmt.Errorf("invalid HMAC signature")
	}

	now := time.Now().UnixNano()

	switch eventType {
	case "deployment_status":
		var p struct {
			TimestampUnixNano int64 `json:"timestamp_unix_nano"`
			Deployment struct {
				ID          int64  `json:"id"`
				SHA         string `json:"sha"`
				Ref         string `json:"ref"`
				Environment string `json:"environment"`
				Description string `json:"description"`
				Creator     struct {
					Login string `json:"login"`
				} `json:"creator"`
			} `json:"deployment"`
			Repository struct {
				Name string `json:"name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("failed to parse deployment_status: %w", err)
		}

		svcName := p.Repository.Name
		if svcName == "" {
			svcName = "app"
		}

		ts := now
		if p.TimestampUnixNano > 0 {
			ts = p.TimestampUnixNano
		}

		return &types.DeployEvent{
			ID:                fmt.Sprintf("deploy-%d", p.Deployment.ID),
			ServiceName:       svcName,
			Environment:       p.Deployment.Environment,
			CommitHash:        p.Deployment.SHA,
			CommitMessage:     p.Deployment.Description,
			Author:            p.Deployment.Creator.Login,
			TimestampUnixNano: ts,
			DiffSummary:       fmt.Sprintf("Deployed ref %s (%s)", p.Deployment.Ref, p.Deployment.Environment),
		}, nil

	case "push":
		var p struct {
			After      string `json:"after"`
			Ref        string `json:"ref"`
			Repository struct {
				Name string `json:"name"`
			} `json:"repository"`
			Pusher struct {
				Name string `json:"name"`
			} `json:"pusher"`
			HeadCommit struct {
				ID      string `json:"id"`
				Message string `json:"message"`
				Author  struct {
					Username string `json:"username"`
				} `json:"author"`
			} `json:"head_commit"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, fmt.Errorf("failed to parse push event: %w", err)
		}

		author := p.HeadCommit.Author.Username
		if author == "" {
			author = p.Pusher.Name
		}
		sha := p.HeadCommit.ID
		if sha == "" {
			sha = p.After
		}

		return &types.DeployEvent{
			ID:                fmt.Sprintf("push-%s", sha[:min(7, len(sha))]),
			ServiceName:       p.Repository.Name,
			Environment:       "production",
			CommitHash:        sha,
			CommitMessage:     p.HeadCommit.Message,
			Author:            author,
			TimestampUnixNano: now,
			DiffSummary:       fmt.Sprintf("Pushed commit %s to %s", sha[:min(7, len(sha))], p.Ref),
		}, nil

	default:
		// Generic deployment payload fallback
		var generic struct {
			ID          string `json:"id"`
			ServiceName string `json:"service_name"`
			CommitHash  string `json:"commit_hash"`
			Message     string `json:"message"`
			Author      string `json:"author"`
			Environment string `json:"environment"`
		}
		if err := json.Unmarshal(payload, &generic); err == nil && generic.ServiceName != "" {
			env := generic.Environment
			if env == "" {
				env = "production"
			}
			return &types.DeployEvent{
				ID:                generic.ID,
				ServiceName:       generic.ServiceName,
				Environment:       env,
				CommitHash:        generic.CommitHash,
				CommitMessage:     generic.Message,
				Author:            generic.Author,
				TimestampUnixNano: now,
			}, nil
		}

		return nil, fmt.Errorf("unsupported webhook event type: %s", eventType)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
