#!/usr/bin/env python3
# genicon.py —— 把 icons/saho.svg 的"数据流水线"设计栅格化成市场图标 PNG。
# 只用 Python 标准库（math/zlib/struct），零第三方依赖，几何与 SVG 逐点对应：
#   渐变圆角方块（#4F46E5 左下 → #0EA5E9 右上）+ 半透明白管道 + 三颗石子 + 流向箭头。
# 用法：python editors/tools/genicon.py
#   生成 editors/vscode/sahou/icons/sahou-128.png 与 sahou-256.png（VS Code 市场要求 PNG ≥128）。
import math
import os
import struct
import zlib

SS = 4  # 每像素采样边长（超采样抗锯齿）

# —— 与 saho.svg（64×64 视口）一致的几何 ——
C1 = (0x4F, 0x46, 0xE5)  # 渐变起点（左下，靛蓝）
C2 = (0x0E, 0xA5, 0xE9)  # 渐变终点（右上，天蓝）
RECT_R = 14.0            # 圆角半径
PIPE_HALF = 6.0          # 管道描边半宽
PIPE_ALPHA = 0.45        # 管道白色不透明度
# 三段直线段
SEGS = [((10.0, 46.0), (30.0, 46.0)),
        ((38.0, 38.0), (38.0, 22.0)),
        ((46.0, 14.0), (54.0, 14.0))]
# 两段四分之一圆弧：(圆心, 半径, 起角, 止角)，角度制，y 向下坐标系（与 SVG 一致）
ARCS = [((38.0, 46.0), 8.0, 180.0, 270.0),
        ((46.0, 22.0), 8.0, 180.0, 270.0)]
# 三颗石子：(圆心, 半径)
STONES = [((14.0, 46.0), 4.2),
          ((28.0, 46.0), 5.2),
          ((46.0, 16.0), 6.5)]
ARROW = [(50.0, 8.0), (56.0, 13.0), (48.0, 16.0)]  # 流动方向小箭头


def clamp(v, lo, hi):
    return lo if v < lo else (hi if v > hi else v)


def dist_segment(px, py, a, b):
    ax, ay = a
    bx, by = b
    dx, dy = bx - ax, by - ay
    l2 = dx * dx + dy * dy
    if l2 == 0:
        return math.hypot(px - ax, py - ay)
    t = clamp(((px - ax) * dx + (py - ay) * dy) / l2, 0.0, 1.0)
    return math.hypot(px - (ax + t * dx), py - (ay + t * dy))


def dist_arc(px, py, center, radius, deg_from, deg_to):
    cx, cy = center
    d = math.hypot(px - cx, py - cy)
    ang = math.degrees(math.atan2(py - cy, px - cx))
    lo, hi = min(deg_from, deg_to), max(deg_from, deg_to)
    if lo <= ang <= hi:
        return abs(d - radius)
    # 角度不在弧段上：退到最近端点
    ex0 = cx + radius * math.cos(math.radians(deg_from))
    ey0 = cy + radius * math.sin(math.radians(deg_from))
    ex1 = cx + radius * math.cos(math.radians(deg_to))
    ey1 = cy + radius * math.sin(math.radians(deg_to))
    return min(math.hypot(px - ex0, py - ey0), math.hypot(px - ex1, py - ey1))


def in_triangle(px, py, tri):
    (ax, ay), (bx, by), (cx, cy) = tri
    d1 = (px - bx) * (ay - by) - (ax - bx) * (py - by)
    d2 = (px - cx) * (by - cy) - (bx - cx) * (py - cy)
    d3 = (px - ax) * (cy - ay) - (cx - ax) * (py - ay)
    has_neg = d1 < 0 or d2 < 0 or d3 < 0
    has_pos = d1 > 0 or d2 > 0 or d3 > 0
    return not (has_neg and has_pos)


def rounded_rect_sdf(px, py, size, r):
    half = size / 2.0
    qx = abs(px - half) - (half - r)
    qy = abs(py - half) - (half - r)
    outside = math.hypot(max(qx, 0.0), max(qy, 0.0))
    return outside + min(max(qx, qy), 0.0) - r


def gradient(px, py, size):
    # SVG linearGradient (0,64)→(64,0)：投影到对角方向后取 0..1
    t = clamp((px + (size - py)) / (2.0 * size), 0.0, 1.0)
    return tuple(C1[i] + (C2[i] - C1[i]) * t for i in range(3))


def sample(px, py, size):
    """一个采样点的 RGBA（0..255，直通 alpha）。px/py 是画布像素，先换算到 64 设计空间。"""
    u = px * 64.0 / size
    v = py * 64.0 / size
    if rounded_rect_sdf(u, v, 64.0, RECT_R) > 0:
        return (0, 0, 0, 0)
    r, g, b = gradient(u, v, 64.0)
    a = 255.0
    # 半透明白管道
    d = min([dist_segment(u, v, s[0], s[1]) for s in SEGS] +
            [dist_arc(u, v, c, r0, f, t) for (c, r0, f, t) in ARCS])
    if d <= PIPE_HALF:
        cov = clamp(PIPE_HALF - d + 0.5, 0.0, 1.0)
        mix = cov * PIPE_ALPHA
        r = r + (255 - r) * mix
        g = g + (255 - g) * mix
        b = b + (255 - b) * mix
    # 三颗石子（不透明白）
    for (cx, cy), r0 in STONES:
        dd = math.hypot(u - cx, v - cy)
        if dd <= r0:
            cov = clamp(r0 - dd + 0.5, 0.0, 1.0)
            r = r + (255 - r) * cov
            g = g + (255 - g) * cov
            b = b + (255 - b) * cov
    # 流向箭头（不透明白）
    if in_triangle(u, v, ARROW):
        r = g = b = 255.0
    return (int(r + 0.5), int(g + 0.5), int(b + 0.5), int(a + 0.5))


def render(size):
    step = 1.0 / SS
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            sr = sg = sb = sa = 0
            for sy in range(SS):
                py = y + (sy + 0.5) * step
                for sx in range(SS):
                    px = x + (sx + 0.5) * step
                    r, g, b, a = sample(px, py, size)
                    sr += r
                    sg += g
                    sb += b
                    sa += a
            n = SS * SS
            row += bytes((sr // n, sg // n, sb // n, sa // n))
        rows.append(bytes(row))
    return rows


def write_png(path, size, rows):
    raw = b"".join(b"\x00" + row for row in rows)  # 每行前置 filter 0
    chunk = struct.pack(">I", size) + struct.pack(">I", size) + bytes([8, 6, 0, 0, 0])

    def crc(data):
        return zlib.crc32(data) & 0xFFFFFFFF

    def piece(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", crc(body))

    png = (b"\x89PNG\r\n\x1a\n"
           + piece(b"IHDR", chunk)
           + piece(b"IDAT", zlib.compress(raw, 9))
           + piece(b"IEND", b""))
    with open(path, "wb") as f:
        f.write(png)


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    editors_dir = os.path.dirname(here)  # editors/
    out_dir = os.path.join(editors_dir, "vscode", "sahou", "icons")
    for size in (128, 256):
        rows = render(size)
        path = os.path.join(out_dir, "sahou-%d.png" % size)
        write_png(path, size, rows)
        print("生成", path, "(%d×%d)" % (size, size))


if __name__ == "__main__":
    main()
