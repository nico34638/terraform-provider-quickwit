package provider

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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

// TestUnconfiguredClient: when the provider configuration is unknown at plan
// time, resources and data sources have no client. Every method that calls
// Quickwit must report it instead of panicking.
func TestUnconfiguredClient(t *testing.T) {
	ctx := context.Background()
	const want = "Quickwit provider not configured"
	check := func(t *testing.T, what string, diags diag.Diagnostics) {
		t.Helper()
		if !diags.HasError() || diags.Errors()[0].Summary() != want {
			t.Errorf("%s: diagnostics = %v, want %q", what, diags, want)
		}
	}
	for _, newResource := range (&quickwitProvider{}).Resources(ctx) {
		r := newResource()
		meta := &resource.MetadataResponse{}
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "quickwit"}, meta)
		t.Run(meta.TypeName, func(t *testing.T) {
			r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{}, &resource.ConfigureResponse{})
			var create resource.CreateResponse
			r.Create(ctx, resource.CreateRequest{}, &create)
			check(t, "Create", create.Diagnostics)
			var read resource.ReadResponse
			r.Read(ctx, resource.ReadRequest{}, &read)
			check(t, "Read", read.Diagnostics)
			var update resource.UpdateResponse
			r.Update(ctx, resource.UpdateRequest{}, &update)
			check(t, "Update", update.Diagnostics)
			var del resource.DeleteResponse
			r.Delete(ctx, resource.DeleteRequest{}, &del)
			check(t, "Delete", del.Diagnostics)
		})
	}
	ds := NewIndexDataSource()
	ds.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{}, &datasource.ConfigureResponse{})
	var read datasource.ReadResponse
	ds.Read(ctx, datasource.ReadRequest{}, &read)
	check(t, "quickwit_index data source Read", read.Diagnostics)
}
