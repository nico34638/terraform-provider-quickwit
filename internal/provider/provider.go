package provider

import (
	"context"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
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
	if cfg.Endpoint.IsUnknown() || cfg.Headers.IsUnknown() || cfg.Timeout.IsUnknown() {
		// Values come from resources not created yet; configure on apply.
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
			resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid timeout", "expected a positive Go duration such as `30s`")
		}
		timeout = d
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

func (p *quickwitProvider) Resources(context.Context) []func() resource.Resource {
	return append([]func() resource.Resource{NewIndexResource}, sourceResources()...)
}

func (p *quickwitProvider) DataSources(context.Context) []func() datasource.DataSource {
	return fieldDataSources()
}
