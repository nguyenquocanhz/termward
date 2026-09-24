# Termward health collector. Runs as `sh -s` over SSH, needs no root and no
# agent. Prints key=value lines; the Go side ignores anything it doesn't know.
export LC_ALL=C

if [ -r /etc/os-release ]; then
	. /etc/os-release
	echo "os=${PRETTY_NAME:-$NAME}"
else
	echo "os=$(uname -s)"
fi
echo "kernel=$(uname -r)"
echo "hostname=$(uname -n)"
echo "nproc=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 1)"
[ -r /proc/uptime ] && echo "uptime=$(cut -d' ' -f1 /proc/uptime)"
[ -r /proc/loadavg ] && echo "load=$(cut -d' ' -f1-3 /proc/loadavg)"

if [ -r /proc/stat ]; then
	echo "cpu0=$(head -n1 /proc/stat)"
	sleep 1
	echo "cpu1=$(head -n1 /proc/stat)"
fi

[ -r /proc/meminfo ] && awk '/^(MemTotal|MemAvailable|MemFree|Buffers|Cached|SwapTotal|SwapFree):/ {sub(":", "", $1); print "mem." $1 "=" $2}' /proc/meminfo

df -P -k 2>/dev/null | awk 'NR > 1 && $1 ~ /^\/dev\// && $1 !~ /^\/dev\/loop/ {print "disk=" $6 "|" $2 "|" $3 "|" $4}'

if command -v systemctl >/dev/null 2>&1; then
	echo "systemd=1"
	systemctl list-units --state=failed --no-legend --plain 2>/dev/null | awk 'NF {print "failed=" $1}'
fi

if command -v docker >/dev/null 2>&1; then
	if out=$(docker ps -a --format '{{.Names}}|{{.State}}|{{.Status}}' 2>/dev/null); then
		echo "docker=ok"
		printf '%s\n' "$out" | awk 'NF {print "ctr=" $0}'
	else
		echo "docker=denied"
	fi
fi

[ -f /var/run/reboot-required ] && echo "reboot=1"
exit 0
