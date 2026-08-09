package main

import (
	"math/big"
)

// marginsFor computes gross margin per channel: (price - food cost) / price.
//
// This is the ONE derived number the service does not compute, and the reason
// is worth stating: a formula reads the BASE scope of its inputs, and a
// scopable attribute has no single base value. `price` is scoped by channel —
// a table, a delivery app and a catering order are the same dish at three
// prices — so `(price - food_cost) / price` has three answers, and the service
// refuses to pretend it has one.
//
// The margin is therefore computed per channel, here, from two values the
// service does provide. See the README.
func marginsFor(foodCost string, prices map[string]string) map[string]string {
	cost, ok := new(big.Rat).SetString(foodCost)
	if !ok || len(prices) == 0 {
		return nil
	}
	out := map[string]string{}
	for channel, raw := range prices {
		price, ok := new(big.Rat).SetString(raw)
		if !ok || price.Sign() == 0 {
			continue
		}
		margin := new(big.Rat).Sub(price, cost)
		margin.Quo(margin, price)
		// Two decimal places: a margin is read as a percentage, and the exact
		// rational is noise at that precision.
		out[channel] = margin.FloatString(4)
	}
	return out
}
