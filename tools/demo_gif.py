# /// script
# requires-python = ">=3.12"
# dependencies = ["pillow>=11"]
# ///
"""Render docs/assets/mkto_demo.gif: a typed terminal session replaying mkto's golden outputs.

The outputs are the files in testdata/golden/, which the test suite writes from the real binary
against the fake Marketo and LLM servers, so every value shown is invented. Run:

    uv run tools/demo_gif.py
"""

import textwrap
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
GOLDEN = ROOT / "testdata" / "golden"
OUT = ROOT / "docs" / "assets" / "mkto_demo.gif"

# shortcut: Ubuntu/Debian font path (fonts-dejavu-core); make it a flag if someone renders on macOS.
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
SIZE, LINE_H, PAD = 15, 20, 18
COLS, ROWS = 100, 26
BG, FG, PROMPT, DIM = (24, 26, 31), (220, 223, 228), (115, 201, 145), (140, 146, 156)


def golden(name: str, first: int | None = None) -> list[str]:
    text = (GOLDEN / name).read_text().replace("<config>", "~/snap/mkto/common/config.toml")
    lines = [w for line in text.splitlines() for w in (textwrap.wrap(line, COLS, subsequent_indent="  ") or [""])]
    return lines[:first] if first else lines


SCENES = [
    ("mkto quota", golden("quota.txt")),
    ("mkto lead jane.doe@example.com", golden("lead_one.txt")),
    ("mkto activity jane.doe@example.com --days 30", golden("activity_one.txt")),
    ("mkto brief jane.doe@example.com", golden("brief.md", ROWS - 3) + ["…"]),
]


def frame(font: ImageFont.FreeTypeFont, typed: str, output: list[str]) -> Image.Image:
    width = PAD * 2 + int(font.getlength("M") * COLS)
    img = Image.new("RGB", (width, PAD * 2 + LINE_H * ROWS), BG)
    d = ImageDraw.Draw(img)
    d.text((PAD, PAD), "$ ", font=font, fill=PROMPT)
    d.text((PAD + font.getlength("$ "), PAD), typed, font=font, fill=FG)
    for i, line in enumerate(output[: ROWS - 1], start=1):
        d.text((PAD, PAD + i * LINE_H), line, font=font, fill=DIM if line.startswith("#") else FG)
    return img


def main() -> None:
    font = ImageFont.truetype(FONT, SIZE)
    frames: list[Image.Image] = []
    durations: list[int] = []
    for cmd, output in SCENES:
        for n in range(0, len(cmd) + 1, 2):  # two characters per frame keeps the file small
            frames.append(frame(font, cmd[:n], []))
            durations.append(45)
        frames.append(frame(font, cmd, []))
        durations.append(350)
        frames.append(frame(font, cmd, output))
        durations.append(3200)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(OUT, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True)
    print(f"wrote {OUT.relative_to(ROOT)} ({len(frames)} frames, {OUT.stat().st_size // 1024} KiB)")


if __name__ == "__main__":
    main()
