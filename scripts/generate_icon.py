#!/usr/bin/env python3
"""生成 rental（租房管理）应用图标：3D 立体玻璃质感。

设计规范见 techfunway-agent 仓库技能 .agents/skills/icon-design/SKILL.md；
设计源是 design/icon/icon.svg（1024 viewBox 矢量，玻璃底板 + 等轴测 3D 小屋
+ 3D 金色 ¥ 徽记），本脚本把它渲染成 design/ 母版 PNG，并分发到 web 与
fnpack 的全部位置。

输出：
  design/icon/icon-{16,32,64,180,256,512,1024}.png    设计母版
  web/public/favicon.svg、favicon-{16,32,192,512}.png、apple-touch-icon.png、favicon.ico
  fnpack/ICON.PNG（64）、ICON_256.PNG（256）、app/ui/images/icon_{64,256}.png

用法：在仓库根目录运行 python3 scripts/generate_icon.py
依赖：rsvg-convert（brew install librsvg）、Pillow。
"""

import shutil
import subprocess
import sys
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "design/icon/icon.svg"

MASTER_SIZES = [16, 32, 64, 180, 256, 512, 1024]
WEB_SIZES = {"favicon-16.png": 16, "favicon-32.png": 32, "favicon-192.png": 192, "favicon-512.png": 512}
ICO_SIZES = [(16, 16), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]


def render(svg: Path, size: int, out: Path):
    out.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["rsvg-convert", "--format", "png", "--width", str(size), "--height", str(size),
         "--output", str(out), str(svg)],
        check=True,
    )


def main():
    rsvg = shutil.which("rsvg-convert")
    if not rsvg:
        sys.exit("缺少 rsvg-convert，请先安装：brew install librsvg")
    if not SRC.exists():
        sys.exit(f"缺少设计源 {SRC}，先定稿 icon.svg 再运行本脚本")

    # 1. 设计母版
    for s in MASTER_SIZES:
        render(SRC, s, ROOT / f"design/icon/icon-{s}.png")

    # 2. web 站点图标
    web = ROOT / "web/public"
    shutil.copyfile(SRC, web / "favicon.svg")
    for name, s in WEB_SIZES.items():
        render(SRC, s, web / name)
    render(SRC, 180, web / "apple-touch-icon.png")
    ico_imgs = []
    for w, h in ICO_SIZES:
        tmp = ROOT / f"tmp/ico-{w}.png"
        render(SRC, w, tmp)
        ico_imgs.append(Image.open(tmp))
    ico_imgs[-1].save(web / "favicon.ico", sizes=ICO_SIZES, append_images=ico_imgs[:-1])

    # 3. fnOS 打包图标
    render(SRC, 64, ROOT / "fnpack/ICON.PNG")
    render(SRC, 256, ROOT / "fnpack/ICON_256.PNG")
    render(SRC, 64, ROOT / "fnpack/app/ui/images/icon_64.png")
    render(SRC, 256, ROOT / "fnpack/app/ui/images/icon_256.png")

    print("icons regenerated (3D glass style)")


if __name__ == "__main__":
    main()
