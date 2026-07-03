#Requires -Version 5.1
<#
.SYNOPSIS
  Full quality run: vet, tests (race+cover), benchmarks, fuzz.
  All output goes to quality.res in the repo root.
#>
param(
    [string]$FuzzTime = "30s",
    [int]$BenchCount = 3,
    [string]$OutFile = "quality.res"
)

$ErrorActionPreference = "Continue"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root
$OutPath = Join-Path $Root $OutFile

$started = Get-Date
$failures = @()

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
    ) | ForEach-Object { Tee-Object -FilePath $OutPath -Append -InputObject $_ }
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
    ) | ForEach-Object { Tee-Object -FilePath $OutPath -Append -InputObject $_ }
    if ($ExitCode -ne 0) {
        $script:failures += $Title
    }
}

function Invoke-QualityCommand {
    param(
        [string]$Title,
        [scriptblock]$Command
    )
    Write-SectionHeader $Title
    $output = & $Command 2>&1
    $exitCode = $LASTEXITCODE
    if ($null -ne $output) {
        $output | ForEach-Object { Tee-Object -FilePath $OutPath -Append -InputObject $_ }
    }
    Write-SectionFooter -Title $Title -ExitCode $exitCode
    return $exitCode
}

# Truncate output file
@(
    "urx quality run"
    "Started: $($started.ToString('yyyy-MM-dd HH:mm:ss'))"
    "Root:    $Root"
    "Go:      $(go version 2>&1)"
    ""
) | Set-Content -Path $OutPath -Encoding utf8

$null = Invoke-QualityCommand "go vet ./..." { go vet ./... }

$null = Invoke-QualityCommand "go test -race -count=1 -timeout=120s -coverprofile=coverage.txt ./..." {
    go test -race -count=1 -timeout=120s -coverprofile=coverage.txt ./...
}

$null = Invoke-QualityCommand "go tool cover -func=coverage.txt" {
    go tool cover -func=coverage.txt
}

$env:GOMAXPROCS = [Environment]::ProcessorCount
$null = Invoke-QualityCommand "go test -bench=Benchmark -benchmem -count=$BenchCount -run='^$' -timeout=30m ./..." {
    go test -bench=Benchmark -benchmem -count=$BenchCount -run='^$' -timeout=30m ./...
}

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
        $null = Invoke-QualityCommand $title {
            go test "-fuzz=^${fuzzFunc}$" "-fuzztime=$FuzzTime" $fuzzDir
        }
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
) | ForEach-Object { Tee-Object -FilePath $OutPath -Append -InputObject $_ }

if ($failures.Count -gt 0) {
    exit 1
}
