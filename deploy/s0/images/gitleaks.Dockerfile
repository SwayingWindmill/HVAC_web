FROM zricethezav/gitleaks:v8.24.2
WORKDIR /repo
COPY . .
RUN gitleaks detect --source=/repo --no-git --redact --verbose --exit-code=1
