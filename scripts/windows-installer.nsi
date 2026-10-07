; Kivo 桌面、CLI 与内嵌 Web 一体安装包。输入目录由构建脚本提供。
Unicode true
!include "MUI2.nsh"
!ifndef PACKAGE_DIR
  !error "PACKAGE_DIR is required"
!endif
!ifndef OUTPUT_FILE
  !error "OUTPUT_FILE is required"
!endif

Name "Kivo"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\Kivo"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Section "安装 Kivo" SecMain
  SetOutPath "$INSTDIR"
  File "${PACKAGE_DIR}\kivo-desktop.exe"
  File "${PACKAGE_DIR}\kivo.exe"
  File "${PACKAGE_DIR}\LICENSE"
  File "${PACKAGE_DIR}\THIRD_PARTY_NOTICES.md"
  File "${PACKAGE_DIR}\THIRD_PARTY_LICENSES.txt"
  File "${PACKAGE_DIR}\桌面端使用说明.md"
  File "${PACKAGE_DIR}\安装与升级说明.md"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateDirectory "$SMPROGRAMS\Kivo"
  CreateShortCut "$SMPROGRAMS\Kivo\Kivo.lnk" "$INSTDIR\kivo-desktop.exe"
  CreateShortCut "$SMPROGRAMS\Kivo\卸载 Kivo.lnk" "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Kivo" "DisplayName" "Kivo"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Kivo" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Kivo" "InstallLocation" "$INSTDIR"
SectionEnd

Section "卸载"
  ; 用户配置与 Mihomo 内核位于独立数据目录，卸载程序不会清理用户数据。
  Delete "$SMPROGRAMS\Kivo\Kivo.lnk"
  Delete "$SMPROGRAMS\Kivo\卸载 Kivo.lnk"
  RMDir "$SMPROGRAMS\Kivo"
  Delete "$INSTDIR\kivo-desktop.exe"
  Delete "$INSTDIR\kivo.exe"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\THIRD_PARTY_LICENSES.txt"
  Delete "$INSTDIR\桌面端使用说明.md"
  Delete "$INSTDIR\安装与升级说明.md"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Kivo"
SectionEnd
