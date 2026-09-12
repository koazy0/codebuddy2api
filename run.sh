#!/bin/bash
cd "$(dirname "$0")"

APP_NAME="codebuddy-gateway"
PID_FILE="./${APP_NAME}.pid"
LOG_FILE="./run.log"
UNIT_NAME="codebuddy-gateway"
ROOT="$(pwd)"

have_systemd() {
  [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1
}

unit_loaded() {
  have_systemd || return 1
  systemctl cat "$UNIT_NAME" >/dev/null 2>&1
}

running_pid() {
  if unit_loaded && systemctl is-active --quiet "$UNIT_NAME"; then
    systemctl show -p MainPID --value "$UNIT_NAME" 2>/dev/null | awk '$1+0>0{print; exit}'
    return 0
  fi
  [ -f "$PID_FILE" ] || return 1
  local pid; pid=$(tr -d '[:space:]' < "$PID_FILE")
  [ -n "$pid" ] || return 1
  if kill -0 "$pid" 2>/dev/null; then
    if tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q "$APP_NAME"; then
      echo "$pid"
      return 0
    fi
  fi
  return 1
}

kill_pid() {
  local pid="$1"
  [ -n "$pid" ] || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  echo "停止进程 PID=$pid ..."
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 20); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.2
  done
  kill -9 "$pid" 2>/dev/null || true
}

stop() {
  if unit_loaded; then
    echo "停止 systemd 服务 $UNIT_NAME ..."
    systemctl stop "$UNIT_NAME" >/dev/null 2>&1 || true
  fi
  local pid leftover
  if pid=$(running_pid); then
    kill_pid "$pid"
  fi
  for leftover in $(pgrep -f "${APP_NAME} server" || true); do
    kill_pid "$leftover"
  done
  rm -f "$PID_FILE"
}

build() {
  echo "编译 $APP_NAME ..."
  CGO_ENABLED=1 go build -buildvcs=false -o "$APP_NAME" .
  if [ $? -ne 0 ]; then
    echo "编译失败"
    exit 1
  fi
  echo "编译成功"
}

install_unit() {
  have_systemd || return 1
  local src="$ROOT/scripts/codebuddy-gateway.local.service"
  [ -f "$src" ] || return 1
  install -m 644 "$src" "/etc/systemd/system/${UNIT_NAME}.service"
  systemctl daemon-reload
}

start() {
  build
  echo "启动 $APP_NAME ..."
  export GATEWAY_PID_FILE="$ROOT/${APP_NAME}.pid"
  if have_systemd; then
    install_unit || true
    if unit_loaded; then
      systemctl enable "$UNIT_NAME" >/dev/null 2>&1 || true
      systemctl restart "$UNIT_NAME"
      sleep 0.4
      if systemctl is-active --quiet "$UNIT_NAME"; then
        echo "启动成功 systemd $UNIT_NAME PID=$(systemctl show -p MainPID --value "$UNIT_NAME")"
        echo "日志: journalctl -u $UNIT_NAME -f   或 tail -f $LOG_FILE"
        return 0
      fi
      echo "systemd 启动失败，journalctl -u $UNIT_NAME -n 40 --no-pager"
      systemctl --no-pager --full status "$UNIT_NAME" || true
      exit 1
    fi
  fi
  stop
  # 兜底：没有 systemd 时再 setsid。注意：从 Codex 回合里启动仍可能被回合回收。
  setsid nohup ./"$APP_NAME" server > "$LOG_FILE" 2>&1 < /dev/null &
  for _ in $(seq 1 25); do
    if pid=$(running_pid); then
      echo "启动成功 PID=$pid"
      echo "日志: tail -f $LOG_FILE"
      return 0
    fi
    sleep 0.2
  done
  echo "启动失败，查看日志: cat $LOG_FILE"
  exit 1
}

restart() { start; }

status() {
  if unit_loaded; then
    systemctl --no-pager --full status "$UNIT_NAME" | head -20
    return 0
  fi
  local pid
  if pid=$(running_pid); then
    echo "运行中 PID=$pid"
    return 0
  fi
  echo "未运行"
  return 1
}

logs() { tail -f "$LOG_FILE"; }

case "${1:-start}" in
  start)   start   ;;
  stop)    stop    ;;
  restart) restart ;;
  status)  status  ;;
  logs)    logs    ;;
  build)   build   ;;
  *)
    echo "用法: $0 {start|stop|restart|status|logs|build}"
    ;;
esac
