package herdr

import (
	"context"
	"os"
)

func RefreshMetadata(ctx context.Context, client *Client, paneID, identity string) error {
	if client == nil {
		client = NewClient(os.Getenv("HERDR_BIN_PATH"))
	}
	return client.ReportIdentity(ctx, paneID, identity)
}

func RefreshMetadataFromEnv(ctx context.Context) error {
	return RefreshMetadata(ctx, NewClient(os.Getenv("HERDR_BIN_PATH")), os.Getenv("HERDR_PANE_ID"), os.Getenv("HERDR_SOURCE_CONTROL_ID"))
}

func RefreshMetadataBestEffort(ctx context.Context, client *Client, paneID, identity string) {
	_ = RefreshMetadata(ctx, client, paneID, identity)
}

func RefreshMetadataFromEnvBestEffort(ctx context.Context) {
	_ = RefreshMetadataFromEnv(ctx)
}
