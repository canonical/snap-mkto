# /// script
# requires-python = ">=3.12"
# dependencies = ["pillow>=11"]
# ///
"""Render the README GIFs in docs/assets/: typed terminal sessions replaying mkto's golden outputs.

The outputs are the testdata/golden/demo_*.txt files, which TestDemoGoldens (cmd/mkto/demo_test.go)
writes from the real binary against the fake Marketo server, so every value shown is invented.
The command lines below are the ones that test runs. Run:

    uv run tools/demo_gif.py
"""

import textwrap
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
GOLDEN = ROOT / "testdata" / "golden"
ASSETS = ROOT / "docs" / "assets"

# shortcut: Ubuntu/Debian font path (fonts-dejavu-core); make it a flag if someone renders on macOS.
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
SIZE, LINE_H, PAD = 15, 20, 18
BG, FG, PROMPT, DIM = (24, 26, 31), (220, 223, 228), (115, 201, 145), (140, 146, 156)


# Each GIF: file name, columns, rows (prompt line included), and (command, golden) scenes.
GIFS = [
    ("mkto_demo.gif", 124, 28, [
        ("mkto doctor", "demo_doctor.txt"),
        ("mkto activity jane.doe@example.com --days 60", "demo_activity.txt"),
        ("mkto why jane.doe@example.com", "demo_why.txt"),
    ]),
    ("mkto_leads.gif", 124, 7, [("mkto leads --from leads.csv --activity", "demo_leads.txt")]),
    ("mkto_account.gif", 100, 9, [("mkto account 001EXAMPLE000001AAA", "demo_account.txt")]),
]


def golden(name: str, cols: int) -> list[str]:
    text = (GOLDEN / name).read_text().replace("<config>", "~/snap/mkto/common/config.toml")
    return [w for line in text.splitlines() for w in (textwrap.wrap(line, cols, subsequent_indent="  ") or [""])]


def frame(font: ImageFont.FreeTypeFont, cols: int, rows: int, typed: str, output: list[str]) -> Image.Image:
    width = PAD * 2 + int(font.getlength("M") * cols)
    img = Image.new("RGB", (width, PAD * 2 + LINE_H * rows), BG)
    d = ImageDraw.Draw(img)
    d.text((PAD, PAD), "$ ", font=font, fill=PROMPT)
    d.text((PAD + font.getlength("$ "), PAD), typed, font=font, fill=FG)
    for i, line in enumerate(output[: rows - 1], start=1):
        d.text((PAD, PAD + i * LINE_H), line, font=font, fill=DIM if line.startswith("#") else FG)
    return img


def render(font: ImageFont.FreeTypeFont, name: str, cols: int, rows: int, scenes: list[tuple[str, str]]) -> None:
    frames: list[Image.Image] = []
    durations: list[int] = []
    for cmd, file in scenes:
        output = golden(file, cols)
        assert len(output) < rows, f"{file}: {len(output)} lines do not fit {rows - 1} rows"
        for n in range(0, len(cmd) + 1, 2):  # two characters per frame keeps the file small
            frames.append(frame(font, cols, rows, cmd[:n], []))
            durations.append(45)
        frames.append(frame(font, cols, rows, cmd, []))
        durations.append(350)
        frames.append(frame(font, cols, rows, cmd, output))
        durations.append(max(3200, 250 * len(output)))  # longer outputs stay up long enough to read
    out = ASSETS / name
    out.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(out, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True)
    print(f"wrote {out.relative_to(ROOT)} ({len(frames)} frames, {out.stat().st_size // 1024} KiB)")


def main() -> None:
    font = ImageFont.truetype(FONT, SIZE)
    for name, cols, rows, scenes in GIFS:
        render(font, name, cols, rows, scenes)


if __name__ == "__main__":
    main()
