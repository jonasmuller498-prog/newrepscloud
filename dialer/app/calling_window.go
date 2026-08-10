package main

import "time"

func withinCallingWindow(now time.Time, timezone string, start, end time.Duration) (bool, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "Local" {
		return false, errInvalidTimezone
	}
	local := now.In(loc)
	offset := time.Duration(local.Hour())*time.Hour +
		time.Duration(local.Minute())*time.Minute +
		time.Duration(local.Second())*time.Second
	return offset >= start && offset < end, nil
}

func nextCallingWindow(now time.Time, timezone string, start time.Duration) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "Local" {
		return time.Time{}, errInvalidTimezone
	}
	local := now.In(loc)
	hour, minute := int(start/time.Hour), int(start%time.Hour/time.Minute)
	target := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !target.After(local) {
		target = time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, loc)
	}
	return target.UTC(), nil
}
