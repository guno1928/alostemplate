#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
count="${COUNT:-5}"
benchtime="${BENCHTIME:-150ms}"
modes="${MODES:-String Buffer}"
binary="$(mktemp -d)/compare.test.exe"
go test -c -o "$binary" .
scenarios="Tiny Small Medium Large Huge LongValues ManySlots"
for mode in $modes; do
	engines="alos_values alos_pairs alos_map quicktemplate fasttemplate text_template"
	if [ "$mode" != "Buffer" ]; then
		engines="alos_values_cached alos_pairs_cached alos_map_cached $engines"
	fi
	engines="${ENGINES:-$engines}"
	for scenario in $scenarios; do
		for engine in $engines; do
			"$binary" -test.run '^$' -test.bench "^Benchmark${mode}\$/^${scenario}\$/^${engine}\$" -test.benchtime="$benchtime" -test.count="$count" | awk -v label="$mode/$scenario/$engine" '
				/^Benchmark/ { ns = $3 + 0; if (best == 0 || ns < best) { best = ns; allocs = $(NF-1) } }
				END { printf "%-40s %12.2f ns  %4s allocs\n", label, best, allocs }'
		done
	done
done
