#!/bin/sh
# Throwaway sheet builder for the bubble-variant comparison (see
# zz-bubble-variants.spec.ts). Run from frontend/.
#
#   sh e2e-visual/build-bv-sheet.sh dark|light         # 4 columns x 3 rows
#   sh e2e-visual/build-bv-sheet.sh focused dark|light # attachments only
set -e
cd "$(dirname "$0")/.."

FONT="/System/Library/Fonts/Hiragino Sans GB.ttc"
S='e2e-visual/shots/bv'

build_main() {
  theme=$1
  bg=$2
  fg=$3
  shift 3
  magick montage \
    -font "$FONT" -pointsize 24 -fill "$fg" -background "$bg" \
    -tile 4x -geometry +10+10 -title "$1" \
    -label '① 短句+截图+文件 · 现状' "$S/1-cluster-base-$theme.png" \
    -label '① · A 减薄' "$S/1-cluster-a-$theme.png" \
    -label '① · B 静默卡+右轨' "$S/1-cluster-b-$theme.png" \
    -label '① · C = A + 附件收进气泡' "$S/1-cluster-c-$theme.png" \
    -label '② 富 Markdown · 现状' "$S/2-markdown-base-$theme.png" \
    -label '② · A 减薄' "$S/2-markdown-a-$theme.png" \
    -label '② · B 静默卡+右轨' "$S/2-markdown-b-$theme.png" \
    -label '② · C（无附件，同 A）' "$S/2-markdown-c-$theme.png" \
    -label '③ 粘贴长需求 · 现状' "$S/3-brief-base-$theme.png" \
    -label '③ · A 减薄' "$S/3-brief-a-$theme.png" \
    -label '③ · B 静默卡+右轨' "$S/3-brief-b-$theme.png" \
    -label '③ · C（无附件，同 A）' "$S/3-brief-c-$theme.png" \
    "$S/zz-sheet-$theme.png"
}

build_focused() {
  theme=$1
  bg=$2
  fg=$3
  magick montage \
    -font "$FONT" -pointsize 26 -fill "$fg" -background "$bg" \
    -tile 3x -geometry +12+12 \
    -title "带附件的用户行：结构那一半单独看（$theme）" \
    -label '现状：三块松散零件' "$S/1-cluster-base-$theme.png" \
    -label 'A：减薄，仍是三块' "$S/1-cluster-a-$theme.png" \
    -label 'C = A + 收进气泡' "$S/1-cluster-c-$theme.png" \
    -label 'B：静默卡+右轨，附件在外' "$S/1-cluster-b-$theme.png" \
    -label 'D = B + 收进气泡' "$S/1-cluster-d-$theme.png" \
    -label '现状（对照重放）' "$S/1-cluster-base-$theme.png" \
    "$S/zz-focused-$theme.png"
}

case "$1" in
  dark)
    build_main dark '#0b0e14' '#d7dbe3' 'OpenCraft 用户气泡 · 深色（列：现状 / A 减薄 / B 静默卡+右轨 / C 减薄+收进气泡）'
    ;;
  light)
    build_main light '#f2f5f9' '#1b2330' 'OpenCraft 用户气泡 · 浅色（列：现状 / A 减薄 / B 静默卡+右轨 / C 减薄+收进气泡）'
    ;;
  focused)
    case "$2" in
      dark) build_focused dark '#0b0e14' '#d7dbe3' ;;
      *) build_focused light '#f2f5f9' '#1b2330' ;;
    esac
    ;;
  *)
    echo "usage: $0 dark|light|focused [dark|light]" >&2
    exit 2
    ;;
esac
