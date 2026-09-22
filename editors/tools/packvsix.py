"""打包 VS Code 扩展 vsix：把 editors/vscode/sahou/ 组装成 sahou-language-<版本>.vsix。

结构对齐仓库里已有的 0.3.0 vsix（vsce 产物）：
  extension.vsixmanifest   —— 从旧 vsix 提取模板，仅替换 Identity 的 Version
  [Content_Types].xml      —— 原样复制
  extension/…              —— 扩展目录内容；CHANGELOG.md→changelog.md、LICENSE→LICENSE.txt
"""
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "editors" / "vscode" / "sahou"
OLD = ROOT / "editors" / "vscode" / "sahou-language-0.3.0.vsix"
OUT = ROOT / "editors" / "vscode" / f"sahou-language-{sys.argv[1]}.vsix"
VERSION = sys.argv[1]

# 文件名映射：源目录名 -> vsix 内 extension/ 下的名字
RENAMES = {"CHANGELOG.md": "changelog.md", "LICENSE": "LICENSE.txt"}
# [Content_Types].xml 里登记过的扩展名之外的类型需要补登记
CONTENT_TYPES_OVERRIDES = {}

with zipfile.ZipFile(OLD) as old:
    manifest = old.read("extension.vsixmanifest").decode("utf-8")
    content_types = old.read("[Content_Types].xml").decode("utf-8")

manifest = manifest.replace(
    f'Id="sahou-language" Version="0.3.0"', f'Id="sahou-language" Version="{VERSION}"'
)
assert f'Version="{VERSION}"' in manifest, "manifest 版本号替换失败"

# 不打进包里的东西：本地扫描缓存目录、vsce 自己也不会带上的忽略清单
EXCLUDE_DIRS = {".mimosa"}
EXCLUDE_FILES = {".vscodeignore"}

files = sorted(
    p
    for p in SRC.rglob("*")
    if p.is_file()
    and p.name not in EXCLUDE_FILES
    and not any(part in EXCLUDE_DIRS for part in p.relative_to(SRC).parts)
)
with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("[Content_Types].xml", content_types)
    z.writestr("extension.vsixmanifest", manifest)
    for p in files:
        rel = p.relative_to(SRC)
        arc = RENAMES.get(rel.name, rel.name)
        z.write(p, f"extension/{(rel.parent / arc).as_posix()}")

with zipfile.ZipFile(OUT) as z:
    pkg = __import__("json").loads(z.read("extension/package.json").decode("utf-8"))
    assert pkg["version"] == VERSION, "扩展 package.json 版本不符"
    print(f"打包完成 {OUT.name}，共 {len(z.namelist())} 个条目，扩展版本 {pkg['version']}")
