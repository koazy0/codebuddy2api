#!/bin/bash
cd "$(dirname "$0")"

APP_NAME="codebuddy-gateway"
PID_FILE="./${APP_NAME}.pid"
LOG_FILE="./run.log"

stop() {
  if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE")
    if kill -0 "$OLD_PID" 2>/dev/null; then
      echo "停止旧进程 PID=$OLD_PID ..."
      kill "$OLD_PID"
      sleep 1
      if kill -0 "$OLD_PID" 2>/dev/null; then
        kill -9 "$OLD_PID"
      fi
    fi
    rm -f "$PID_FILE"
  fi
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
  nohup ./"$APP_NAME" server > "$LOG_FILE" 2>&1 &
  echo $! > "$PID_FILE"
  sleep 1
  if kill -0 $(cat "$PID_FILE") 2>/dev/null; then
    echo "启动成功 PID=$(cat $PID_FILE)"
    echo "日志: tail -f $LOG_FILE"
  else
    echo "启动失败，查看日志: cat $LOG_FILE"
    exit 1
  fi
}

restart() { start; }

status() {
  if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
      echo "运行中 PID=$PID"
      return 0
    fi
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
