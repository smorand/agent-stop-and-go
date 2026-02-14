# Agent Stop and Go -- Documentation

Comprehensive documentation for the Agent Stop and Go project, a generic API for building async autonomous agents with MCP tool support, A2A sub-agent delegation, and human-in-the-loop approval workflows.

## Table of Contents

| Document | Description |
|----------|-------------|
| [Project Overview](overview.md) | Purpose, key features, tech stack, quick start guide, project structure |
| [Architecture](architecture.md) | System architecture diagrams, component relationships, data models, design decisions |
| [Functionalities](functionalities.md) | Detailed feature documentation: LLM, MCP, A2A, approvals, orchestration, API reference |
| [Authentication](authentication.md) | Bearer token forwarding, session ID tracing, security considerations |
| [DevOps](devops.md) | Build system, testing strategy, code quality tools, Docker build, logging |
| [Deployment](deployment.md) | Local development, Docker single-agent, Docker Compose multi-agent |

## Recommended Reading Order

1. **[Overview](overview.md)** -- Start here to understand what the project does and how to run it
2. **[Architecture](architecture.md)** -- Understand the system design, component interactions, and data flow
3. **[Functionalities](functionalities.md)** -- Deep dive into each feature with configuration examples
4. **[Authentication](authentication.md)** -- Learn how auth forwarding and session tracing work
5. **[DevOps](devops.md)** -- Build, test, and quality tools
6. **[Deployment](deployment.md)** -- Deploy locally, with Docker, or with Docker Compose

## Additional Resources

| Resource | Location | Description |
|----------|----------|-------------|
| README | [/README.md](/README.md) | Project README with usage examples and curl commands |
| CLAUDE.md | [/CLAUDE.md](/CLAUDE.md) | AI-oriented project reference |
| Agent Docs | [/.agent_docs/](/README.md) | Agent-specific coding standards and patterns |
| Examples | [/examples/](/examples/README.md) | Ready-to-use configuration examples with test prompts |
| API Docs | http://localhost:8080/docs | Interactive HTML documentation (when agent is running) |
