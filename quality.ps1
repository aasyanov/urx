#Requires -Version 5.1
<#
.SYNOPSIS
  Full quality run: vet, golangci-lint, tests (race+cover), benchmarks, fuzz.
  All output goes to quality.result in the repo root.
#>
param(
    [string]$FuzzTime = "30s",
    [int]$BenchCount = 3,
    [string]$OutFile = "quality.result"
)

$ErrorActionPreference = "Continue"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root
$OutPath = Join-Path $Root $OutFile

$started = Get-Date
$failures = @()

function Write-Tee {
    param(
        [Parameter(ValueFromPipeline = $true)]
        $InputObject
    )
    process {
        if ($null -eq $InputObject) { return }
        foreach ($item in @($InputObject)) {
            $text = if ($item -is [System.Management.Automation.ErrorRecord]) {
                $item.ToString()
            } else {
                [string]$item
            }
            Add-Content -Path $OutPath -Value $text -Encoding utf8
            Write-Host $text
        }
    }
}

function Write-SectionHeader {
    param([string]$Title)
    $line = "=" * 72
    @(
        ""
        $line
        $Title
        "Started: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
        $line
        ""
    ) | Write-Tee
}

function Write-SectionFooter {
    param(
        [string]$Title,
        [int]$ExitCode
    )
    @(
        ""
        "Exit code: $ExitCode"
        "Finished: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
        ""
    ) | Write-Tee
    if ($ExitCode -ne 0) {
        $script:failures += $Title
        Write-Host "FAILED: $Title" -ForegroundColor Red
    }
}

function Invoke-QualityCommand {
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$Title,
        [Parameter(Mandatory = $true, Position = 1)]
        [string]$Exe,
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$CommandArgs
    )
    Write-SectionHeader $Title
    & $Exe @CommandArgs 2>&1 | ForEach-Object {
        Write-Tee -InputObject $_
    }
    $exitCode = $LASTEXITCODE
    Write-SectionFooter -Title $Title -ExitCode $exitCode
    return $exitCode
}

# Truncate output file, then mirror header to console
Set-Content -Path $OutPath -Value '' -Encoding utf8
@(
    "urx quality run"
    "Started: $($started.ToString('yyyy-MM-dd HH:mm:ss'))"
    "Root:    $Root"
    "Go:      $(go version 2>&1)"
    ""
) | Write-Tee

Invoke-QualityCommand "go vet ./..." go vet './...' | Out-Null

Invoke-QualityCommand "golangci-lint run ./..." golangci-lint run './...' | Out-Null

Invoke-QualityCommand 'go test -race -count=1 -timeout=120s -coverprofile="coverage.txt" ./...' `
    go test -race -count=1 -timeout=120s '-coverprofile=coverage.txt' './...' | Out-Null

Invoke-QualityCommand 'go tool cover -func="coverage.txt"' go tool cover '-func=coverage.txt' | Out-Null

$env:GOMAXPROCS = [Environment]::ProcessorCount
Invoke-QualityCommand "go test -bench . -benchmem -count=$BenchCount -run='^$' -timeout=30m ./..." `
    go test -benchmem "-count=$BenchCount" -run='^$' -timeout=30m -bench . './...' | Out-Null

$fuzzFiles = Get-ChildItem -Path $Root -Recurse -Filter '*_test.go' |
    Where-Object { $_.FullName -notmatch '\\vendor\\|\\\.git\\' } |
    Where-Object { Select-String -Path $_.FullName -Pattern '^func Fuzz' -Quiet }

foreach ($file in $fuzzFiles) {
    $dir = $file.DirectoryName
    $relDir = Resolve-Path -Relative $dir
    $funcs = Select-String -Path $file.FullName -Pattern '^func (Fuzz\w+)' |
        ForEach-Object { $_.Matches.Groups[1].Value }

    foreach ($func in $funcs) {
        $fuzzFunc = $func
        $fuzzDir = $dir
        $title = "go test -fuzz=^${fuzzFunc}$ -fuzztime=$FuzzTime $relDir"
        Invoke-QualityCommand $title go test "-fuzz=^${fuzzFunc}$" "-fuzztime=$FuzzTime" $fuzzDir | Out-Null
    }
}

$finished = Get-Date
$duration = $finished - $started

@(
    ""
    ("=" * 72)
    "SUMMARY"
    ("=" * 72)
    "Started:  $($started.ToString('yyyy-MM-dd HH:mm:ss'))"
    "Finished: $($finished.ToString('yyyy-MM-dd HH:mm:ss'))"
    "Duration: $($duration.ToString())"
    "Output:   $OutPath"
    if ($failures.Count -eq 0) {
        "Result:   ALL PASSED"
    } else {
        "Result:   FAILED ($($failures.Count) section(s))"
        "Failed sections:"
        ($failures | ForEach-Object { "  - $_" })
    }
    ""
) | Write-Tee

if ($failures.Count -gt 0) {
    Write-Host ""
    Write-Host "Quality run finished with $($failures.Count) failure(s). See $OutPath" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Quality run finished: ALL PASSED" -ForegroundColor Green
