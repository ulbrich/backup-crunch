@echo off
setlocal EnableExtensions

rem ===========================================================================
rem  gather-data.bat
rem
rem  Copies a named folder from the current Windows user's profile to a target
rem  drive, into a sub-folder named after the user. Uses robocopy so it handles
rem  paths longer than Explorer's 260-character limit.
rem
rem  Usage:   gather-data.bat <root-dir-name> <target-path>
rem  Example: gather-data.bat "Foobar\Whatever" D:
rem
rem    user   = %USERNAME%                  (taken automatically, e.g. JaneDoe)
rem    source = C:\Users\<user>\<root-dir-name>
rem    dest   = <target-path>\<user>        (e.g. D:\JaneDoe)
rem ===========================================================================

rem --- require exactly two arguments: the root dir name and the target path ---
if "%~1"=="" (
    echo Usage: %~nx0 ^<root-dir-name^> ^<target-path^>
    echo   e.g. %~nx0 "Foobar\Whatever" D:
    exit /b 1
)
if "%~2"=="" (
    echo Usage: %~nx0 ^<root-dir-name^> ^<target-path^>
    echo   e.g. %~nx0 "Foobar\Whatever" D:
    exit /b 1
)
if not "%~3"=="" (
    echo Error: too many arguments - provide exactly a root dir name and a target path.
    echo Usage: %~nx0 ^<root-dir-name^> ^<target-path^>
    exit /b 1
)

rem --- user name from the environment ---
set "NAME=%USERNAME%"

rem --- root directory name to copy from the user's profile ---
set "ROOTDIR=%~1"

rem --- normalize target: drop a trailing backslash so "D:\" and "D:" both work ---
set "TARGET=%~2"
if "%TARGET:~-1%"=="\" set "TARGET=%TARGET:~0,-1%"

set "SRC=C:\Users\%NAME%\%ROOTDIR%"
set "DST=%TARGET%\%NAME%"
set "LOG=%TARGET%\robocopy-%NAME%.log"

rem --- verify the source folder exists ---
if not exist "%SRC%\" (
    echo Error: source folder not found:
    echo   "%SRC%"
    exit /b 1
)

echo Copying:
echo   from: "%SRC%"
echo   to:   "%DST%"
echo   log:  "%LOG%"
echo.

rem  /E         copy all subdirectories, including empty ones
rem  /COPY:DAT  copy Data, Attributes and Timestamps (no ACLs/owner - cross-machine noise)
rem  /DCOPY:DAT copy directory timestamps
rem  /R:1 /W:1  retry once, wait 1s (do not hang on a locked file)
rem  /XJ        skip junctions / reparse points (avoids loops)
rem  /XA:O      skip OFFLINE files = dehydrated OneDrive "online-only" placeholders
rem             (remove this if you DO want robocopy to download them)
rem  /MT:16     16 copy threads (faster on an SSD)
rem  /TEE       show progress on screen and in the log
rem  /LOG       write a log next to the per-user folder on the target drive
robocopy "%SRC%" "%DST%" /E /COPY:DAT /DCOPY:DAT /R:1 /W:1 /XJ /XA:O /MT:16 /TEE /LOG:"%LOG%"

rem  robocopy exit codes: 0-7 = success (bit-coded), 8 and above = failure.
set "RC=%ERRORLEVEL%"
echo.
if %RC% GEQ 8 (
    echo robocopy reported errors ^(exit code %RC%^). See "%LOG%"
    exit /b %RC%
)
echo Done ^(robocopy exit code %RC%^). Log: "%LOG%"
exit /b 0
