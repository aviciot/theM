#!/bin/bash
set -e
read -s -p "GitHub token: " GH_TOKEN
echo
git push "https://aviciot:${GH_TOKEN}@github.com/aviciot/them.git" main
