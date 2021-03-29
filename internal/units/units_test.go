package units

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

const (
	rawValue  = "12340000000000000000000000000000"
	bananoValue = "12.34"
)

func TestBananoToRaw(t *testing.T) {
	i := BananoToRaw(decimal.RequireFromString(bananoValue))
	assert.Equal(t, rawValue, i.String())
}

func TestRawToBanano(t *testing.T) {
	i := decimal.RequireFromString(rawValue)
	assert.Equal(t, bananoValue, RawToBanano(i).String())
}
