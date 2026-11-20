; LPS Windows installer (Inno Setup 6).
; Build: iscc /DAppVersion=1.2.3 installer\windows\lps.iss
; Expects bin\lps.exe and bin\lpsd.exe (see build.ps1 "installer" task).
; Installs per user (no admin rights) into %LOCALAPPDATA%\Programs\LPS.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef BinDir
  #define BinDir "..\..\bin"
#endif

[Setup]
AppId={{8F3C2B1E-6A4D-4F7B-9E2A-1C5D7B9E0A11}
AppName=LPS - Local Personal Server
AppVersion={#AppVersion}
AppVerName=LPS {#AppVersion}
AppPublisher=LPS contributors
AppPublisherURL=https://github.com/AbrarDW/lps
AppSupportURL=https://github.com/AbrarDW/lps/issues
AppUpdatesURL=https://github.com/AbrarDW/lps/releases
DefaultDirName={localappdata}\Programs\LPS
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=..\..\dist
OutputBaseFilename=LPS-Setup-{#AppVersion}
SetupIconFile=..\..\cmd\lps\lps.ico
UninstallDisplayIcon={app}\lps.exe
UninstallDisplayName=LPS - Local Personal Server
LicenseFile=..\..\LICENSE
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "autostart"; Description: "Start LPS when I sign in to Windows (recommended)"; GroupDescription: "Startup:"
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#BinDir}\lps.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#BinDir}\lpsd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\LPS"; Filename: "{app}\lps.exe"; Comment: "Open LPS - Local Personal Server"
Name: "{autoprograms}\LPS (command line)"; Filename: "{cmd}"; Parameters: "/k ""{app}\lpsd.exe"" help"; WorkingDir: "{app}"; Comment: "lpsd command-line tool"
Name: "{autodesktop}\LPS"; Filename: "{app}\lps.exe"; Tasks: desktopicon

[Registry]
; Start at sign-in (also toggled later from the tray menu / dashboard).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "LPS"; ValueData: """{app}\lps.exe"""; Tasks: autostart; Flags: uninsdeletevalue
; Remove the value on uninstall even if it was enabled later from inside LPS.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: none; ValueName: "LPS"; Flags: uninsdeletevalue

[Run]
Filename: "{app}\lps.exe"; Description: "Launch LPS and open the setup page"; Flags: nowait postinstall skipifsilent
; Updates started from the LPS dashboard run silently with /RELAUNCH=1: start
; the new version again (without opening another browser tab).
Filename: "{app}\lps.exe"; Parameters: "--after-update"; Flags: nowait; Check: RelaunchAfterUpdate

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM lps.exe"; Flags: runhidden; RunOnceId: "StopLPS"

[Code]
procedure StopLPS();
var
  ResultCode: Integer;
begin
  { Ignore the result: taskkill fails harmlessly when LPS is not running. }
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM lps.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  { Upgrades: stop the running copy so its files can be replaced. }
  StopLPS();
  Result := '';
end;

function RelaunchAfterUpdate(): Boolean;
begin
  Result := ExpandConstant('{param:relaunch|0}') = '1';
end;

function DockerDesktopInstalled(): Boolean;
begin
  Result := FileExists(ExpandConstant('{commonpf64}\Docker\Docker\Docker Desktop.exe'));
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  if (CurPageID = wpFinished) and not DockerDesktopInstalled() then
    WizardForm.FinishedLabel.Caption := WizardForm.FinishedLabel.Caption + #13#10#13#10 +
      'Note: Docker Desktop was not found. LPS needs it to run Linux VPSes and apps. ' +
      'The LPS setup page that opens next shows how to install it.';
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if (CurUninstallStep = usPostUninstall) and not UninstallSilent() then
    MsgBox('LPS was removed.' + #13#10#13#10 +
      'Your LPS data (database and settings) was kept in ' + ExpandConstant('{commonappdata}') + '\LPS, ' +
      'so reinstalling keeps your VPSes and apps. Delete that folder to remove it completely.' + #13#10#13#10 +
      'VPS and app containers are still in Docker Desktop.', mbInformation, MB_OK);
end;
