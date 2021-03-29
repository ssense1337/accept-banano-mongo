package units

import (
	"github.com/shopspring/decimal"
)

const exp = 30

func BananoToRaw(banano decimal.Decimal) decimal.Decimal {
	return banano.Shift(exp)
}

func RawToBanano(raw decimal.Decimal) decimal.Decimal {
	return raw.Shift(-exp)
}
