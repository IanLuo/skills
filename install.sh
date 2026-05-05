#!/bin/bash
# install.sh — install the pipeline system into any coding agent project
# Usage:
#   curl -sSL https://raw.githubusercontent.com/.../main/install.sh | bash
#   or: bash install.sh
#
# This is a minimal bootstrap that clones the full pipeline repo,
# then copies files into your target project.
set -euo pipefail

REPO_URL="${PIPELINE_REPO_URL:-https://github.com/IanLuo/skills.git}"
PIPELINE_SRC=""

echo "============================================"
echo " Pipeline System — Agent-Agnostic Installer"
echo "============================================"
echo ""

# ── Step 1: Find or clone the pipeline source ──────────────────────────

find_source() {
    # Check if we're already inside the pipeline repo
    if [ -f "install.sh" ] && [ -d "pipeline/scripts" ] && [ -d "pipeline/skills" ]; then
        PIPELINE_SRC="$(pwd)"
        echo "[install] Detected pipeline source at: $PIPELINE_SRC"
        return 0
    fi

    # Check if there's a local clone
    for candidate in "./skills" "../skills" "$HOME/skills" "/tmp/pipeline-skills"; do
        if [ -d "$candidate/pipeline/scripts" ] && [ -d "$candidate/pipeline/skills" ]; then
            PIPELINE_SRC="$(cd "$candidate" && pwd)"
            echo "[install] Found pipeline source at: $PIPELINE_SRC"
            return 0
        fi
    done

    # Clone it
    CLONE_DIR="/tmp/pipeline-skills-$$"
    echo "[install] Cloning pipeline source from $REPO_URL ..."
    git clone --depth 1 "$REPO_URL" "$CLONE_DIR" 2>/dev/null || {
        echo "[install] ERROR: Could not clone repo."
        echo "  Set PIPELINE_REPO_URL to your fork, or clone manually and re-run from inside the repo."
        exit 1
    }
    PIPELINE_SRC="$CLONE_DIR"
    echo "[install] Cloned to: $PIPELINE_SRC"
}

find_source

# ── Step 2: Determine target project ───────────────────────────────────

echo ""
read -r -p "Target project directory [default: current]: " TARGET
TARGET="${TARGET:-.}"

# Resolve to absolute path
TARGET="$(cd "$TARGET" 2>/dev/null && pwd)" || {
    echo "[install] ERROR: Directory '$TARGET' does not exist."
    exit 1
}
echo "[install] Target: $TARGET"

# ── Step 3: Detect or ask for coding agent ────────────────────────────

echo ""
echo "Which coding agent are you using?"
echo "  Known agents: opencode | claude | cursor"
echo "  (Type 'other' to specify a custom skills directory)"
read -r -p "Agent [default: opencode]: " AGENT
AGENT="${AGENT:-opencode}"

# ── Step 4: Determine install paths per agent ──────────────────────────

case "$AGENT" in
    opencode)
        SKILLS_DIR="$TARGET/.opencode/skills"
        ;;
    claude|claude-code)
        SKILLS_DIR="$TARGET/.claude/skills"
        ;;
    cursor)
        SKILLS_DIR="$TARGET/.cursor/skills"
        ;;
    other|custom)
        read -r -p "Path to skills directory (relative to project, e.g. .myagent/skills): " SKILLS_REL
        SKILLS_DIR="$TARGET/$SKILLS_REL"
        ;;
    *)
        echo "[install] Unknown agent '$AGENT'. Treating as custom."
        read -r -p "Path to skills directory (relative to project, e.g. .myagent/skills): " SKILLS_REL
        SKILLS_DIR="$TARGET/$SKILLS_REL"
        ;;
esac

SCRIPTS_DIR="$TARGET/scripts/pipeline"

echo ""
echo "Install paths:"
echo "  Skills  → $SKILLS_DIR"
echo "  Scripts → $SCRIPTS_DIR"
echo "  Docs    → $TARGET/docs/"
echo ""

read -r -p "Proceed with install? [Y/n]: " CONFIRM
CONFIRM="${CONFIRM:-Y}"
if [ "$CONFIRM" != "Y" ] && [ "$CONFIRM" != "y" ]; then
    echo "[install] Aborted."
    exit 0
fi

# ── Step 5: Copy files ─────────────────────────────────────────────────

echo ""
echo "[install] Copying skills ..."
mkdir -p "$SKILLS_DIR"
for skill in "$PIPELINE_SRC/pipeline/skills/"*; do
    skill_name="$(basename "$skill")"
    if [ -d "$SKILLS_DIR/$skill_name" ]; then
        echo "  ⚠ $skill_name already exists — overwriting"
    fi
    cp -r "$skill" "$SKILLS_DIR/"
done
echo "[install] Skills installed to $SKILLS_DIR"

echo ""
echo "[install] Copying scripts ..."
mkdir -p "$SCRIPTS_DIR"
cp "$PIPELINE_SRC/pipeline/scripts/"*.sh "$SCRIPTS_DIR/"
cp "$PIPELINE_SRC/pipeline/scripts/pipeline.json" "$SCRIPTS_DIR/"
cp -r "$PIPELINE_SRC/pipeline/scripts/schemas" "$SCRIPTS_DIR/"
echo "[install] Scripts installed to $SCRIPTS_DIR"

# ── Step 6: Patch placeholders in all skill files ───────────────────────

echo ""
echo "[install] Patching agent-specific paths ..."

# The placeholder {PIPELINE_SKILLS_DIR} appears in SKILL.md, README.md, etc.
# We need the relative path from project root to skills dir
SKILLS_REL="${SKILLS_DIR#$TARGET/}"

# Determine sed -i syntax (macOS vs Linux)
SED_INPLACE=(-i '')
if [[ "$(uname -s)" != "Darwin" ]]; then
    SED_INPLACE=(-i)
fi

# Find all files containing the placeholder and patch them
patched=0
while IFS= read -r file; do
    [ -z "$file" ] && continue
    sed "${SED_INPLACE[@]}" "s|{PIPELINE_SKILLS_DIR}|$SKILLS_REL|g" "$file"
    echo "  ✓ Patched ${file#$TARGET/}"
    patched=$((patched + 1))
done < <(grep -rl '{PIPELINE_SKILLS_DIR}' "$SKILLS_DIR" 2>/dev/null || true)

if [ "$patched" -eq 0 ]; then
    echo "  (no files contained the placeholder)"
fi

# ── Step 7: Bootstrap project (init equivalent) ────────────────────────

echo ""
echo "[install] Bootstrapping project ..."

mkdir -p "$TARGET/docs/tasks"
mkdir -p "$TARGET/docs/prd"
mkdir -p "$TARGET/docs/qa"
mkdir -p "$TARGET/docs/pipeline"
mkdir -p "$TARGET/docs/ui-ux/screens"
mkdir -p "$TARGET/docs/ui-ux/assets"

PLAN="$TARGET/docs/tasks/plan.md"
HEADER="## Pipeline Status: v1
- [ ] PRD approved             —                  → step-1-prd
- [ ] Architecture updated      —                  → step-2a-architecture
- [ ] UI/UX updated             —                  → step-2b-designer
- [ ] Design reviewed           —                  → step-2-review
- [ ] Plan updated              —                  → step-3-planner
- [ ] Cross-review passed       —                  → step-4-cross-review
- [ ] QA test cases drafted     —                  → step-5-qa-design
- [ ] Development               —                  → step-6-dev
- [ ] QA verified               —                  → step-7-qa-exec"

if [ -f "$PLAN" ] && grep -q "Pipeline Status" "$PLAN" 2>/dev/null; then
    echo "  plan.md already has pipeline header — skipping"
else
    if [ -f "$PLAN" ]; then
        tmp=$(mktemp)
        echo "$HEADER" > "$tmp"
        echo "" >> "$tmp"
        cat "$PLAN" >> "$tmp"
        mv "$tmp" "$PLAN"
    else
        echo "# Action Plan" > "$PLAN"
        echo "" >> "$PLAN"
        echo "$HEADER" >> "$PLAN"
        echo "" >> "$PLAN"
    fi
    echo "  ✓ $PLAN — pipeline header seeded"
fi

# ── Step 8: Done ───────────────────────────────────────────────────────

echo ""
echo "============================================"
echo " Pipeline installed successfully!"
echo "============================================"
echo ""
echo "  To run:  /pipeline-orchestrator run"
echo "  Or step: /pipeline-orchestrator step"
echo ""
echo "  Docs dir:     $TARGET/docs/"
echo "  Skills dir:   $SKILLS_DIR"
echo "  Scripts dir:  $SCRIPTS_DIR"
echo "  Config:       $SCRIPTS_DIR/pipeline.json"
echo ""
