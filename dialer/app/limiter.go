package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func takeCPSToken(ctx context.Context, tx pgx.Tx, cps float64) (bool, time.Time, error) {
	if cps <= 0 {
		return false, time.Time{}, nil
	}
	var next time.Time
	err := tx.QueryRow(ctx, `UPDATE cps_limiter SET theoretical_arrival=
		GREATEST(theoretical_arrival,clock_timestamp())+($1 * interval '1 second')
		WHERE limiter_key='global' AND theoretical_arrival<=clock_timestamp()
		RETURNING theoretical_arrival`, 1/cps).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT theoretical_arrival FROM cps_limiter
			WHERE limiter_key='global'`).Scan(&next)
		return false, next, err
	}
	return err == nil, next, err
}

func gcraNext(now, theoretical time.Time, cps float64) (bool, time.Time) {
	if cps <= 0 {
		return false, theoretical
	}
	if theoretical.After(now) {
		return false, theoretical
	}
	base := now
	if theoretical.After(base) {
		base = theoretical
	}
	return true, base.Add(time.Duration(float64(time.Second) / cps))
}
