package mapping

import "testing"

// Expected results come from Quickwit's prepend_at_char + cron 0.17
// (cron::Schedule::from_str).
func TestValidateRetentionSchedule_cron(t *testing.T) {
	for _, s := range []string{
		"hourly", " daily ", "weekly", "monthly", "yearly", "@hourly",
		"0 0 * * * *", "0 0 3 * * *", "0 0 3 * * * 2030", "0 */15 * * * *",
		"0 0 0 1 jan-mar *", "0 0 0 1 JAN *", "0 0 0 ? * MON-FRI", "0 0 0 ? * mon,wed,fri",
		"0 0 0 ? * ?", "0 0 0 1 1-12/2 *", "0 0 0 1 1 ? 2100", "0 0 1 - 5 * * *",
	} {
		if err := ValidateRetentionSchedule(s); err != nil {
			t.Errorf("ValidateRetentionSchedule(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{
		"", "@", "Hourly", "annually", "@hourly x",
		"0 0 * * *",         // 5 fields: cron 0.17 requires seconds
		"* * * * * * * *",   // 8 fields
		"0 0 24 * * *",      // hour out of range
		"60 * * * * *",      // second out of range
		"0 0 0 * * 0",       // days of week are 1-7
		"0 0 0 1 1 ? 2101",  // year out of range
		"0 0 0 5-1 * *",     // decreasing range
		"0 */60 * * * *",    // step larger than the field
		"0 0 0 L * *",       // unsupported syntax
		"? 0 0 * * *",       // `?` only for days
		"0 0 0 1 foo *",     // unknown month
		"0 0 0 * * 0/0",     // zero step
		"jan 0 0 * * *",     // names only for months and days of week
	} {
		if err := ValidateRetentionSchedule(s); err == nil {
			t.Errorf("ValidateRetentionSchedule(%q) = nil, want an error", s)
		}
	}
}
