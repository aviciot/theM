#!/bin/bash
set -e
read -s -p "GitHub token: " GH_TOKEN
echo
git remote set-url origin "https://aviciot:${GH_TOKEN}@github.com/aviciot/them.git"
echo "Remote updated. Run: git push origin main"
