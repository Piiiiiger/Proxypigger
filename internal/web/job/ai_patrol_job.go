package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"time"
)

type AIPatrolJob struct{}

func (*AIPatrolJob) Run() { service.RunAIPatrol(time.Now()) }
