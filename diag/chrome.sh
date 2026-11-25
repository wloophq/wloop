#!/bin/bash
# Throwaway diagnostic: times headless Chrome --dump-dom per flag variant on a CI runner.
set -u
C="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
"$C" --version
N=${N:-60}
out=${OUT:-diag-out}; mkdir -p "$out"
flags() {
  case $1 in
    base) echo "" ;;
    mock) echo "--use-mock-keychain" ;;
    quiet) echo "--no-first-run --disable-background-networking --disable-component-update" ;;
    both) echo "--use-mock-keychain --no-first-run --disable-background-networking --disable-component-update" ;;
  esac
}
for i in $(seq 1 "$N"); do
  for v in base mock quiet both; do
    d=$(mktemp -d)
    echo '<!doctype html><pre id="probe">x</pre>' > "$d/p.html"
    s=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
    # shellcheck disable=SC2086
    perl -e 'alarm shift; exec @ARGV' 60 "$C" --headless=new --disable-gpu --allow-file-access-from-files --user-data-dir="$d/profile" --window-size=800,800 \
      $(flags $v) --enable-logging=stderr --v=1 --dump-dom "file://$d/p.html" >"$d/out" 2>"$d/err"
    rc=$?
    e=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
    os=$(grep -oE "OSCrypt.AsyncInitialization.Time recorded 1 samples, mean = [0-9.]+" "$d/err" | awk '{print $NF}')
    dur=$(echo "$e - $s" | bc)
    echo "$v $dur rc=$rc oscrypt=${os:-none}" | tee -a "$out/times.txt"
    if [ "$rc" != 0 ] || [ "$(echo "$dur > 10" | bc)" = 1 ]; then
      cp "$d/err" "$out/slow-$v-$i.log"
    fi
    rm -rf "$d"
  done
done
for v in base mock quiet both; do
  awk -v v=$v '$1==v{print $2}' "$out/times.txt" | sort -n | awk -v v=$v '{a[NR]=$1} END{printf "%s n=%d p50=%s p90=%s p99=%s max=%s\n", v, NR, a[int(NR*.5)+1], a[int(NR*.9)], a[int(NR*.99)], a[NR]}'
  awk -v v=$v '$1==v && $3!="rc=0"' "$out/times.txt"
done
