package units

import (
	"github.com/shopspring/decimal"
)

const exp = 30

func BananoToRaw(nano decimal.Decimal) decimal.Decimal {
	return nano.Shift(exp)
}

func RawToBanano(raw decimal.Decimal) decimal.Decimal {
	return raw.Shift(-exp)
}
