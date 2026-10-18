package main

import "better34/internal"

func getEnv(key string) string {
	return internal.GetEnv(key)
}
