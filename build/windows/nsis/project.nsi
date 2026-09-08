Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails3 task windows:package ARCH=amd64
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "v3spike"
## !define INFO_COMPANYNAME    "My Company" # Default "My Company"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "My Product"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© 2026, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"
!include "LogicLib.nsh"
!include "Win\COM.nsh"
!include "Win\Propkey.nsh"
!ifndef STGM_READWRITE
    !define STGM_READWRITE 2
!endif

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

# setShortcutAppUserModelID stamps the System.AppUserModel.ID property onto
# an existing shortcut. Windows toast notifications from unpackaged apps are
# attributed through the AppID the notifications service sends with; the
# Wails v3 service uses application.Options.Name ("OpenCraft"), so the
# shortcuts created below must carry the same AppUserModelID or the toasts
# land under a separate, unbranded entry. The write is best-effort: failures
# only affect notification attribution and never abort the installation.
#
# Usage:
#   Push "$SMPROGRAMS\OpenCraft.lnk" ; shortcut file
#   Push "OpenCraft"                 ; AppUserModelID
#   Call setShortcutAppUserModelID
Function setShortcutAppUserModelID
	Pop $R1 ; AppUserModelID
	Pop $R0 ; shortcut path

	System::Store S
	; $0 HRESULT, $1 IShellLink, $2 IPersistFile, $3 IPropertyStore,
	; $4 PROPERTYKEY, $5 PROPVARIANT, $6 wide-string AppID buffer
	IntOp $0 0 - 1
	IntOp $1 0 + 0
	IntOp $2 0 + 0
	IntOp $3 0 + 0
	IntOp $4 0 + 0
	IntOp $5 0 + 0
	IntOp $6 0 + 0

	!insertmacro ComHlpr_CreateInProcInstance ${CLSID_ShellLink} ${IID_IShellLink} r1 ".r0"
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: create ShellLink failed ($0), skipping AppUserModelID"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	${IUnknown::QueryInterface} $1 '("${IID_IPersistFile}",.r2)i.r0'
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: IPersistFile unavailable ($0), skipping AppUserModelID"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	${IPersistFile::Load} $2 '("$R0",${STGM_READWRITE})i.r0'
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: cannot open $R0 ($0), skipping AppUserModelID"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	${IUnknown::QueryInterface} $1 '("${IID_IPropertyStore}",.r3)i.r0'
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: IPropertyStore unavailable ($0), skipping AppUserModelID"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	System::Call '*${SYSSTRUCT_PROPERTYKEY}(${PKEY_AppUserModel_ID})p.r4'
	StrLen $7 "$R1"
	IntOp $7 $7 + 1 ; trailing NUL
	IntOp $7 $7 * 2 ; UTF-16
	System::Call "ole32::CoTaskMemAlloc(i $7)p.r6"
	${If} $6 = 0
		DetailPrint "OpenCraft installer: no memory for AppUserModelID, skipping"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}
	System::Call '*$6(&w$7 "$R1")'
	System::Call '*${SYSSTRUCT_PROPVARIANT}(${VT_LPWSTR},,p r6)p.r5'

	${IPropertyStore::SetValue} $3 '($4,$5)i.r0'
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: set AppUserModelID failed ($0), skipping"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	${IPropertyStore::Commit} $3 "i.r0"
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: commit AppUserModelID failed ($0), skipping"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	${IPersistFile::Save} $2 '("$R0",1)r.r0'
	${If} $0 <> 0
		DetailPrint "OpenCraft installer: save AppUserModelID failed ($0), skipping"
		Goto setShortcutAppUserModelIDFailed
	${EndIf}

	DetailPrint "OpenCraft installer: AppUserModelID set on $R0"
	Goto setShortcutAppUserModelIDDone

setShortcutAppUserModelIDFailed:
	DetailPrint "OpenCraft installer: could not set AppUserModelID on $R0; installation continues"

setShortcutAppUserModelIDDone:
	${If} $6 <> 0
		System::Call "ole32::CoTaskMemFree(p r6)"
	${EndIf}
	${If} $5 <> 0
		System::Free $5
	${EndIf}
	${If} $4 <> 0
		System::Free $4
	${EndIf}
	${If} $3 <> 0
		${IUnknown::Release} $3 ""
	${EndIf}
	${If} $2 <> 0
		${IUnknown::Release} $2 ""
	${EndIf}
	${If} $1 <> 0
		${IUnknown::Release} $1 ""
	${EndIf}
	System::Store L
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    
    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Push "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Push "${INFO_PRODUCTNAME}"
    Call setShortcutAppUserModelID
    Push "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    Push "${INFO_PRODUCTNAME}"
    Call setShortcutAppUserModelID

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    
    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall" 
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    DeleteRegKey HKCU "Software\Classes\AppUserModelId\${INFO_PRODUCTNAME}"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
