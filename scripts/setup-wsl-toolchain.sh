#!/bin/bash
# 发布工具链准备：fnpack CLI + zig 交叉编译器（均装到用户目录，无需 sudo）
set -e
mkdir -p $HOME/bin $HOME/tools
cd $HOME/tools

# ---------- fnpack CLI 1.2.3 ----------
if [ ! -x $HOME/bin/fnpack ]; then
  echo "== 下载 fnpack CLI =="
  curl -sL --max-time 300 -o fnpack https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64
  chmod +x fnpack
  mv fnpack $HOME/bin/fnpack
fi
$HOME/bin/fnpack --help | head -5

# ---------- zig 交叉编译器 ----------
if [ ! -d $HOME/tools/zig ]; then
  echo "== 下载 zig 0.13.0（官方源）=="
  curl -sL --max-time 900 -o zig.tar.xz https://ziglang.org/download/0.13.0/zig-linux-x86_64-0.13.0.tar.xz
  tar -xJf zig.tar.xz
  mv zig-linux-x86_64-0.13.0 zig
  rm zig.tar.xz
fi
$HOME/tools/zig/zig version
echo "TOOLCHAIN-OK"
