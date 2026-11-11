# Eval Suite

The eval suite applies broken manifests to a [kind](https://kind.sigs.k8s.io/) cluster, runs `kubectl-why`, and scores the output for:

| Metric | Points |
|--------|--------|
| Correct root-cause category | 1 |
| Each fix keyword present | 0.5 |
| Each hallucinated term | −1 |

## Running

```bash
# 1. Start a kind cluster
kind create cluster

# 2. Build kubectl-why
go build -o kubectl-why .

# 3. Run the harness
go run ./eval/harness --scenarios ./eval/scenarios --binary ./kubectl-why
```

## Scenarios

| # | Name | Expected Category |
|---|------|------------------|
| 01 | bad-image | ImagePullBackOff |
| 02 | oomkilled | OOMKilled |
| 03 | unschedulable | FailedScheduling |

## Sample Score Table

```
SCENARIO         CATEGORY_OK  FIX    HALLUC  SCORE  ERROR
01-bad-image     ✓            3/4    0       0.85
02-oomkilled     ✓            4/5    0       0.80
03-unschedulable ✓            4/5    0       0.80
```

*(Scores improve with an LLM provider; run without `--no-llm` for richer analysis.)*
