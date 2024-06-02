# Set execution policy and security protocol
Set-ExecutionPolicy Bypass -Scope Process -Force
[System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072

# Install Chocolatey
Invoke-Expression ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))

# Install packages using Chocolatey
choco install git.install --version=2.25.1 -y --no-progress
choco install golang --version=1.15 -y --no-progress
choco install nomad --version=1.0.4 -y --no-progress

# Stop and restart Nomad service
Stop-Service nomad
Get-CimInstance win32_service -filter "name='nomad'" | Invoke-CimMethod -Name Change -Arguments @{StartName="LocalSystem"} | Out-Null

# Configure Nomad directories and copy files
$nomadDir = "C:\\ProgramData\\nomad"
New-Item -ItemType Directory -Path "$nomadDir\\plugin" -Force
Copy-Item "C:\\vagrant\\win_iis.exe" -Destination "$nomadDir\\plugin" -Force
Copy-Item "C:\\vagrant\\test\\win_client.hcl" -Destination "$nomadDir\\conf\\client.hcl" -Force

# Start Nomad service
Start-Service nomad

# Import PFX certificate
Import-PfxCertificate -FilePath "C:\\vagrant\\test\\test.pfx" -CertStoreLocation Cert:\\LocalMachine\\My -Password (ConvertTo-SecureString -String 'Test123!' -AsPlainText -Force)

# Install IIS (Internet Information Services) feature
Install-WindowsFeature -Name Web-Server -IncludeAllSubFeature -IncludeManagementTools -Restart
