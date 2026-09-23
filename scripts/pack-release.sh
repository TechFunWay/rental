#!/bin/bash
# 组装 release/<版本> 产物：archives + fnOS fpk + SHA256SUMS
set -e
ROOT=/mnt/f/workspace/techfunway/gitea/rental
VER=$(cat $ROOT/VERSION | tr -d 'v
')
REL=$ROOT/release/$VER
PKG=techfunway-rental

rm -rf "$REL"
mkdir -p "$REL"

# ---------- Windows zip ----------
STAGE="$REL/rental-windows-amd64"
mkdir -p "$STAGE"
cp "$ROOT/server/rental-windows-amd64.exe" "$STAGE/rental.exe"
cp -r "$ROOT/server/static/dist" "$STAGE/www"
cd "$REL"
zip -qr rental-windows-amd64.zip rental-windows-amd64
rm -rf "$STAGE"

# ---------- Linux tar.gz ----------
STAGE="$REL/rental-linux-amd64"
mkdir -p "$STAGE"
cp "$ROOT/server/rental-linux-amd64" "$STAGE/rental"
chmod +x "$STAGE/rental"
cp -r "$ROOT/server/static/dist" "$STAGE/www"
COPYFILE_DISABLE=1 tar czf rental-linux-amd64.tar.gz rental-linux-amd64
rm -rf "$STAGE"

# ---------- fnOS fpk（amd64，手工组装等价于 fnpack build）----------
STAGE="$REL/_fpk_stage"
mkdir -p "$STAGE"
cp -r "$ROOT/fnpack/cmd" "$ROOT/fnpack/config" "$ROOT/fnpack/wizard" "$STAGE/"
mkdir -p "$STAGE/app"
cp "$ROOT/server/rental-linux-amd64" "$STAGE/app/rental"
chmod +x "$STAGE/app/rental"
cp -r "$ROOT/fnpack/app/ui" "$STAGE/app/"
cp -r "$ROOT/server/static/dist/." "$STAGE/app/ui/"
cp "$ROOT/fnpack/ICON.PNG" "$ROOT/fnpack/ICON_256.PNG" "$STAGE/"
# manifest：platform=x86（fnOS 惯例，amd64 对应 x86），版本取自 VERSION 文件
sed "s/^version.*/version               = ${VER}/" "$ROOT/fnpack/manifest" > "$STAGE/manifest"
cd "$STAGE"
COPYFILE_DISABLE=1 tar czf "$REL/${PKG}_amd64.fpk" .
cd "$REL"
rm -rf "$STAGE"

# ---------- 校验和 ----------
sha256sum rental-windows-amd64.zip rental-linux-amd64.tar.gz ${PKG}_amd64.fpk > SHA256SUMS.txt

echo "==== release/$VER ===="
ls -lh
echo "==== SHA256 ===="
cat SHA256SUMS.txt
