package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestSourceParamsRoundTrip decodes params as Quickwit returns them, encodes
// them back and checks nothing is lost. It covers Pulsar, which the
// acceptance tests cannot reach.
func TestSourceParamsRoundTrip(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		model sourceModel
		raw   string
	}{
		{"pulsar token", &pulsarModel{}, `{"address":"pulsar://b:6650","authentication":{"token":"secret"},"consumer_name":"quickwit","topics":["a","b"]}`},
		{"pulsar oauth2", &pulsarModel{}, `{"address":"pulsar+ssl://b:6651","authentication":{"oauth2":{"audience":null,"credentials_url":"file:///c.json","issuer_url":"https://i","scope":"s"}},"consumer_name":"qw","topics":["a"]}`},
		{"pulsar no auth", &pulsarModel{}, `{"address":"pulsar://b:6650","consumer_name":"quickwit","topics":["a"]}`},
		{"kafka", &kafkaModel{}, `{"client_log_level":"warn","client_params":{"bootstrap.servers":"k:9092","fetch.min.bytes":"1"},"enable_backfill_mode":true,"topic":"t"}`},
		{"kinesis", &kinesisModel{}, `{"enable_backfill_mode":false,"region":"eu-west-1","stream_name":"s"}`},
		{"pubsub", &pubsubModel{}, `{"credentials_file":null,"enable_backfill_mode":false,"max_messages_per_pull":10,"project_id":"p","subscription":"s"}`},
		{"sqs", &sqsModel{}, `{"notifications":[{"deduplication_cleanup_interval_secs":60,"deduplication_window_duration_secs":3600,"deduplication_window_max_messages":100000,"message_type":"raw_uri","queue_url":"http://q","type":"sqs"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if diags := tc.model.setParams(ctx, json.RawMessage(tc.raw)); diags.HasError() {
				t.Fatal(diags)
			}
			p, diags := tc.model.params(ctx)
			if diags.HasError() {
				t.Fatal(diags)
			}
			b, _ := json.Marshal(p)
			var got, want any
			_ = json.Unmarshal(b, &got)
			_ = json.Unmarshal([]byte(tc.raw), &want)
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Fatalf("\n got: %s\nwant: %s", gb, wb)
			}
		})
	}
}

func TestSourceSchemas(t *testing.T) {
	ctx := context.Background()
	for _, f := range sourceResources() {
		r := f()
		resp := &resource.SchemaResponse{}
		r.Schema(ctx, resource.SchemaRequest{}, resp)
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("schema: %v", diags)
		}
	}
}
