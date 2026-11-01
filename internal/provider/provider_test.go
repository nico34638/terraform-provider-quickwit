package provider

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"quickwit": providerserver.NewProtocol6WithError(New("test")()),
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("QUICKWIT_ENDPOINT") == "" {
		t.Setenv("QUICKWIT_ENDPOINT", defaultEndpoint)
	}
}

// TestIndexSchema checks the schema (and its defaults) is internally
// consistent; it runs without Quickwit or Terraform.
func TestIndexSchema(t *testing.T) {
	ctx := context.Background()
	resp := &resource.SchemaResponse{}
	NewIndexResource().Schema(ctx, resource.SchemaRequest{}, resp)
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("schema: %v", diags)
	}
}
