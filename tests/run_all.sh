#!/bin/bash
# 全量回归（除 wasm.sh 单独跑过）
cd "$(dirname "$0")/.." || exit 1
for t in run build modules stdlib packages pack net fullstack app; do
  if bash "tests/$t.sh" > "/tmp/t_$t.out" 2>&1; then
    echo "[$t] OK -- $(tail -1 /tmp/t_$t.out)"
  else
    echo "[$t] FAILED"
    tail -8 "/tmp/t_$t.out"
  fi
done
echo "== gofmt =="
if [ -z "$(gofmt -l main.go internal cmd)" ]; then echo "gofmt OK"; else gofmt -l main.go internal cmd; fi
echo "== go test =="
go test ./internal/... 2>&1 | tail -3
