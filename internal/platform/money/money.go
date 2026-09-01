package money

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("valor monetário inválido")

func ParseDecimal(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrInvalid
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return 0, ErrInvalid
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		return 0, ErrInvalid
	}

	reais, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}

	var centavos int64
	if hasFrac {
		switch len(fracPart) {
		case 1:
			fracPart += "0"
		case 2:
		default:
			return 0, ErrInvalid
		}
		centavos, err = strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, ErrInvalid
		}
	}

	cents := reais*100 + centavos
	if cents < 0 || cents/100 != reais {
		return 0, ErrInvalid
	}
	return cents, nil
}

func FormatCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := strconv.FormatInt(cents/100, 10) + "." + leftPad2(cents%100)
	if neg {
		return "-" + s
	}
	return s
}

func leftPad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
