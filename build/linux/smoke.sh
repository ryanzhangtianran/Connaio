#!/bin/sh
# smoke.sh starts the installed connaio under a virtual display and session bus, and passes once its
# core answers on the controller socket and proxies a request through the mixed-port.
dbus-run-session -- xvfb-run -a sh -c '
	connaio > /tmp/connaio.log 2>&1 &
	pid=$!
	ok=1
	for i in $(seq 40); do
		if curl -sf --unix-socket "$HOME/.config/Connaio/core.sock" http://x/version; then ok=0; break; fi
		sleep 1
	done
	echo
	code=$(curl -s -o /dev/null -w "%{http_code}" -x http://127.0.0.1:7890 http://www.gstatic.com/generate_204)
	echo "through mixed-port: $code"
	[ "$code" = 204 ] || ok=1
	kill $pid
	exit $ok'
status=$?
cat /tmp/connaio.log
exit $status
