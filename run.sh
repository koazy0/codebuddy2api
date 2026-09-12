#!/bin/bash
cd "$(dirname "$0")"

APP_NAME="codebuddy-gateway"
PID_FILE="./${APP_NAME}.pid"
LOG_FILE="./run.log"

running_pid() {
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
  local pid leftover
  if pid=$(running_pid); then
    kill_pid "$pid"
  fi
  # pid 文件可能是 setsid 的短命父进程，再按二进制清一次，避免 8088 被残留占用。
  for leftover in $(pgrep -f "./${APP_NAME} server" || true); do
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

start() {
  stop
  build
  echo "启动 $APP_NAME ..."
  export GATEWAY_PID_FILE="$(pwd)/${APP_NAME}.pid"
  # setsid：脱离 Codex/SSH 的 PTY 进程组，避免会话结束把网关带走。
  # 进程自己会写 GATEWAY_PID_FILE（真实 PID，不是 setsid 父进程）。
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
