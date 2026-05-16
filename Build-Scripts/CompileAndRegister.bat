@echo off
echo ========================================================
echo   Tally WhatsApp Sender - Compiler & COM Register utility 
echo ========================================================
echo.

echo [1/3] Checking if TallyPrime is running...
tasklist /fi "imagename eq tally.exe" | find /i "tally.exe" > nul
if %errorlevel% equ 0 (
    echo [ERROR] Tally Prime is currently running!
    echo Please close Tally COMPLETELY before continuing.
    pause
    exit /b
)

echo [2/3] Compiling the C# Solution...
set MSBUILD_PATH=C:\Windows\Microsoft.NET\Framework\v4.0.30319\MSBuild.exe
%MSBUILD_PATH% "%~dp0..\Tally-COM-Interface\TallyWhatsappsender\TallyWhatsappsender.csproj" /p:Configuration=Release /t:Rebuild

echo.
echo [3/3] Registering the new DLL with Windows COM...
set REGASM_64=C:\Windows\Microsoft.NET\Framework64\v4.0.30319\regasm.exe
set REGASM_32=C:\Windows\Microsoft.NET\Framework\v4.0.30319\regasm.exe
set DLL_PATH="%~dp0..\Tally-COM-Interface\TallyWhatsappsender\bin\Release\TallyWhatsappsender.dll"

echo Unregistering old versions...
%REGASM_64% %DLL_PATH% /unregister /silent
%REGASM_32% %DLL_PATH% /unregister /silent

echo Registering new version for 64-bit (TallyPrime)...
%REGASM_64% %DLL_PATH% /codebase /tlb
echo.
echo Registering new version for 32-bit (Older Tally / Compatibility)...
%REGASM_32% %DLL_PATH% /codebase /tlb

echo.
echo ========================================================
echo DONE! The C# DLL has been successfully baked and registered.
echo You can now restart Tally!
echo ========================================================
pause
