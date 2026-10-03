package main

import (
	"math/big"
	"testing"
)

var moneyResult any
var integerResult *big.Int

func BenchmarkDecimalAdd(b *testing.B) {
	a := makeMoney(12345, 2).(_math_Decimal)
	c := makeMoney(750, 3).(_math_Decimal)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		moneyResult = addMoney(a, c)
	}
}

func BenchmarkDecimalDiv(b *testing.B) {
	a := makeMoney(12345, 2).(_math_Decimal)
	c := makeMoney(3, 0).(_math_Decimal)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		moneyResult = divideMoney(a, c)
	}
}

func BenchmarkBigIntAddBaseline(b *testing.B) {
	a, c := big.NewInt(123450), big.NewInt(750)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		integerResult = new(big.Int).Add(a, c)
	}
}
