package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type AIPatrolJob struct{}

func (*AIPatrolJob) Run() { service.RunAIPatrol(time.Now()) }
