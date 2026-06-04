package ocpp

// RedactIDTag masks the middle of an idTag / IdToken value for log lines.
// The first 2 and last 2 characters are kept; shorter strings are fully
// masked. PII such as RFID serial numbers and account identifiers
// should never appear in plain text in operator logs.
func RedactIDTag(s string) string {
	const mask = "***"
	if len(s) <= 4 {
		return mask
	}
	return s[:2] + mask + s[len(s)-2:]
}
