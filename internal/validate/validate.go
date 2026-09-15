package validate

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

func UUID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' && id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{}
}
func Airport(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, c := range value {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
func Name(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 100 && !strings.ContainsAny(value, "\x00\r\n")
}
