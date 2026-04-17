package pihole

//go:generate go run ../../tools/pihole-metricgen -spec https://raw.githubusercontent.com/pi-hole/FTL/master/src/api/docs/content/specs/main.yaml -out metrics_gen.go -package pihole
