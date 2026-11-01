package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
)

const (
	defaultEndpoint = "http://localhost:7280"
	defaultTimeout  = 30 * time.Second
)

var _ provider.Provider = (*quickwitProvider)(nil)

type quickwitProvider struct {
	version string
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &quickwitProvider{version: version} }
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Headers  types.Map    `tfsdk:"headers"`
	Timeout  types.String `tfsdk:"timeout"`
}

func (p *quickwitProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "quickwit"
	resp.Version = p.version
}

func (p *quickwitProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages [Quickwit](https://quickwit.io) indexes. Quickwit has no native authentication: " +
			"use `headers` to authenticate against a reverse proxy.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Quickwit REST endpoint, without the `/api/v1` suffix. Defaults to the `QUICKWIT_ENDPOINT` environment variable, then `" + defaultEndpoint + "`.",
			},
			"headers": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Extra HTTP headers sent with every request, e.g. `Authorization` for an authenticating proxy.",
			},
			"timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "HTTP request timeout, as a Go duration (`30s`, `2m`). Defaults to `30s`.",
			},
		},
	}
}

func (p *quickwitProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Endpoint.IsUnknown() || cfg.Timeout.IsUnknown() || hasUnknownElement(cfg.Headers) {
		// Values come from resources not created yet: no client until apply.
		// Resources and data sources report it if they need one (configured).
		return
	}

	endpoint := os.Getenv("QUICKWIT_ENDPOINT")
	if !cfg.Endpoint.IsNull() {
		endpoint = cfg.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	headers := map[string]string{}
	resp.Diagnostics.Append(cfg.Headers.ElementsAs(ctx, &headers, false)...)

	timeout := defaultTimeout
	if !cfg.Timeout.IsNull() {
		d, err := time.ParseDuration(cfg.Timeout.ValueString())
		if err != nil || d <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid timeout",
				fmt.Sprintf("expected a positive Go duration such as `30s`, got %q", cfg.Timeout.ValueString()))
		} else {
			timeout = d
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(client.Config{Endpoint: endpoint, Headers: headers, Timeout: timeout})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid endpoint", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func hasUnknownElement(m types.Map) bool {
	if m.IsUnknown() {
		return true
	}
	for _, v := range m.Elements() {
		if v.IsUnknown() {
			return true
		}
	}
	return false
}

// clientFromProviderData is shared by the Configure methods of resources and
// data sources. ProviderData is nil before the provider is configured, and
// stays nil when its configuration is not known yet.
func clientFromProviderData(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data",
			fmt.Sprintf("expected *client.Client, got %T. This is a bug in the provider, please report it.", data))
	}
	return c
}

// configured reports an error when c is nil, i.e. when the provider
// configuration depends on values only known after apply. Every method that
// calls Quickwit checks it first.
func configured(c *client.Client, diags *diag.Diagnostics) bool {
	if c != nil {
		return true
	}
	diags.AddError("Quickwit provider not configured",
		"The quickwit provider configuration (endpoint, headers or timeout) depends on values that are not known yet, "+
			"such as attributes of resources not created yet. Create those first (e.g. `terraform apply -target=...`), "+
			"or configure the provider with values known at plan time.")
	return false
}

func (p *quickwitProvider) Resources(context.Context) []func() resource.Resource {
	return append([]func() resource.Resource{NewIndexResource}, sourceResources()...)
}

func (p *quickwitProvider) DataSources(context.Context) []func() datasource.DataSource {
	return append([]func() datasource.DataSource{NewIndexDataSource}, fieldDataSources()...)
}
