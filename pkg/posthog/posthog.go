package posthog

import "github.com/posthog/posthog-go"

type PosthogClient struct {
	client posthog.Client
}

func NewPosthogClient(client posthog.Client) *PosthogClient {
	return &PosthogClient{client: client}
}
