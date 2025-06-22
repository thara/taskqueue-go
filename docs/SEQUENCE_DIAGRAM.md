# Task Execution Sequence Diagram

## Full Task Lifecycle

```mermaid
sequenceDiagram
    participant S as Scheduler
    participant R as Redis
    participant TD as Task Distributor
    participant Q as Message Queue
    participant W as Worker
    participant API as External API

    loop Every check interval
        S->>S: Check task intervals
        S->>R: Get last execution times
        R-->>S: Return timestamps
        S->>S: Identify due tasks
        
        alt Tasks are due
            S->>R: Query enabled users
            R-->>S: User preferences
            S->>TD: Send batch (task_type, user_ids[])
            
            TD->>TD: Calculate time slots
            loop For each user
                TD->>TD: Assign user to slot
                TD->>Q: Push task with execution_time
            end
            
            TD-->>S: Distribution complete
            S->>R: Update last execution time
        end
    end

    loop Worker process
        W->>Q: Pull next task
        Q-->>W: Task details
        
        W->>R: Request rate limit token
        alt Token available
            R-->>W: Token granted
            W->>API: Execute task
            API-->>W: Response
            W->>Q: Acknowledge task
            W->>R: Record metrics
        else Rate limit exceeded
            R-->>W: Wait required
            W->>W: Sleep and retry
        end
    end
```

## Rate Limiting Detail

```mermaid
sequenceDiagram
    participant W1 as Worker 1
    participant W2 as Worker 2
    participant R as Redis
    participant TB as Token Bucket

    Note over TB: 100 tokens/sec capacity

    W1->>R: EVAL rate_limit.lua
    R->>TB: Check tokens
    TB-->>R: 1 token available
    R-->>W1: Token granted
    
    W2->>R: EVAL rate_limit.lua
    R->>TB: Check tokens
    TB-->>R: 0 tokens available
    R-->>W2: Wait 10ms
    
    Note over TB: 10ms passes, 1 token added
    
    W2->>R: EVAL rate_limit.lua (retry)
    R->>TB: Check tokens
    TB-->>R: 1 token available
    R-->>W2: Token granted
```

## Task Distribution Algorithm

```mermaid
flowchart TB
    A[Scheduler triggers task type X] --> B[Get enabled users]
    B --> C{User count?}
    C -->|< 100| D[No distribution needed]
    C -->|>= 100| E[Calculate slots]
    
    E --> F[Window = 30 minutes]
    F --> G[Slots = Window / 10 seconds]
    G --> H[Users per slot = Total / Slots]
    
    H --> I[For each user]
    I --> J[Hash user ID]
    J --> K[Slot = Hash % Total Slots]
    K --> L[Execution Time = Now + Slot * 10s]
    L --> M[Push to queue]
    
    D --> N[Execution Time = Now]
    N --> M
```

## Failure Recovery Sequences

### Scheduler Crash Recovery

```mermaid
sequenceDiagram
    participant S1 as Scheduler 1
    participant R as Redis
    participant S2 as Scheduler 2
    
    S1->>R: Acquire lock "scheduler:leader"
    R-->>S1: Lock acquired
    S1->>R: Update heartbeat
    Note over S1: Crash occurs
    
    S2->>R: Try acquire lock "scheduler:leader"
    R-->>S2: Lock held by S1
    S2->>R: Check S1 heartbeat
    R-->>S2: Heartbeat expired
    S2->>R: Force acquire lock
    R-->>S2: Lock acquired
    S2->>R: Get last execution times
    R-->>S2: State recovered
    S2->>S2: Resume scheduling
```

### Worker Task Retry

```mermaid
sequenceDiagram
    participant W as Worker
    participant Q as Queue
    participant API as External API
    
    W->>Q: Pull task
    Q-->>W: Task (attempt 1)
    W->>API: Execute
    Note over W,API: Timeout/Error
    W->>Q: Negative acknowledge
    
    Note over Q: Wait retry delay
    
    W->>Q: Pull task
    Q-->>W: Task (attempt 2)
    W->>API: Execute
    API-->>W: Success
    W->>Q: Acknowledge
```

## Redis Data Structures

```mermaid
graph LR
    A[Redis Keys] --> B[taskqueue:schedule:*]
    A --> C[taskqueue:users:*]
    A --> D[taskqueue:ratelimit:*]
    A --> E[taskqueue:metrics:*]
    
    B --> B1[Last execution times]
    C --> C1[User task preferences]
    D --> D1[Token bucket state]
    E --> E1[Execution counters]
```