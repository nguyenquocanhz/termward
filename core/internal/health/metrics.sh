# Termward live-metrics loop. Runs as `sh -s <interval>` over a pooled SSH
# session (no agent, nothing installed). Every <interval> seconds it reads only
# /proc (read-only) and prints ONE flushed line:
#
#   TWM <epoch_s> <cpu_total> <cpu_idle> <memTotalKb> <memAvailableKb> \
#       <load1> <load5> <load15> <swapTotalKb> <swapFreeKb> <procsRunning> <ncpu>
#
# Portable to dash and busybox ash. One awk invocation per tick does the work
# (and, exiting each tick, flushes the line); the shell only times the loop. A
# broken stdout (SSH channel closed) kills awk, which stops the loop, and a
# max-tick backstop guarantees the remote process cannot loop forever even if
# teardown is missed.
export LC_ALL=C

INTERVAL=${1:-2}
# Guard against a non-numeric or sub-second interval.
[ "$INTERVAL" -ge 1 ] 2>/dev/null || INTERVAL=2

# Backstop: at most this many ticks before the loop exits on its own. The core
# restarts the stream while the host stays connected and monitored.
MAX=1800

i=0
while [ "$i" -lt "$MAX" ]; do
	now=$(date +%s 2>/dev/null) || now=0
	# A single awk pass over the three proc files. cpu_total = sum of every
	# numeric field on the first "cpu " line; cpu_idle = idle + iowait (the 4th
	# and 5th numbers after "cpu"). MemAvailable is approximated from
	# MemFree+Buffers+Cached on kernels older than 3.14 that lack it.
	awk -v now="$now" '
		FILENAME ~ /stat$/ {
			if ($1 == "cpu") {
				t = 0
				for (n = 2; n <= NF; n++) t += $n
				cpu_total = t
				cpu_idle = $5 + $6
			} else if ($1 ~ /^cpu[0-9]+$/) {
				ncpu++
			} else if ($1 == "procs_running") {
				procs_stat = $2
			}
			next
		}
		FILENAME ~ /meminfo$/ {
			if ($1 == "MemTotal:") mt = $2
			else if ($1 == "MemAvailable:") { ma = $2; have_ma = 1 }
			else if ($1 == "MemFree:") mf = $2
			else if ($1 == "Buffers:") bf = $2
			else if ($1 == "Cached:" && $1 !~ /^SwapCached/) ca = $2
			else if ($1 == "SwapTotal:") st = $2
			else if ($1 == "SwapFree:") sf = $2
			next
		}
		FILENAME ~ /loadavg$/ {
			l1 = $1; l5 = $2; l15 = $3
			split($4, r, "/")
			procs_load = r[1]
			next
		}
		END {
			if (!have_ma) ma = mf + bf + ca
			pr = (procs_load != "") ? procs_load : procs_stat
			printf "TWM %s %s %s %s %s %s %s %s %s %s %s %s\n", \
				now + 0, cpu_total + 0, cpu_idle + 0, mt + 0, ma + 0, \
				l1 + 0, l5 + 0, l15 + 0, st + 0, sf + 0, pr + 0, ncpu + 0
		}
	' /proc/stat /proc/meminfo /proc/loadavg 2>/dev/null || exit 0
	i=$((i + 1))
	sleep "$INTERVAL" 2>/dev/null || exit 0
done
