@echo off
chcp 65001 >nul
title MoreLink Windows
echo Starting MoreLink...
echo Web UI will open at http://127.0.0.1:47890
start "" http://127.0.0.1:47890
"%~dp0..\dist\MoreLink-Windows.exe"
pause
