package planttwin

import (
	"context"
	"fmt"
	"time"
)

// StaticLoader is an in-memory Loader backed by defensive clones of prebuilt SiteTwins.
type StaticLoader struct {
	twins map[SiteScope]SiteTwin
}

// NewStaticLoader validates each twin's scope and hydraulic topology and returns
// an isolated in-memory Loader.
func NewStaticLoader(twins ...SiteTwin) (*StaticLoader, error) {
	indexed := make(map[SiteScope]SiteTwin, len(twins))
	for _, twin := range twins {
		if err := twin.Scope.Validate(); err != nil {
			return nil, err
		}
		built, err := NewSiteTwin(twin)
		if err != nil {
			return nil, err
		}
		normScope := built.Scope.normalized()
		built.Scope = normScope
		indexed[normScope] = built
	}
	return &StaticLoader{twins: indexed}, nil
}

// LoadSiteTwin returns a defensively cloned SiteTwin with a rebuilt HydraulicGraph.
func (l *StaticLoader) LoadSiteTwin(ctx context.Context, scope SiteScope, at time.Time) (SiteTwin, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return SiteTwin{}, err
		}
	}
	if err := scope.Validate(); err != nil {
		return SiteTwin{}, err
	}
	if l == nil {
		return SiteTwin{}, fmt.Errorf("%w: tenant=%s site=%s", ErrSiteNotFound, scope.TenantID, scope.SiteID)
	}
	normScope := scope.normalized()
	stored, ok := l.twins[normScope]
	if !ok {
		return SiteTwin{}, fmt.Errorf("%w: tenant=%s site=%s", ErrSiteNotFound, normScope.TenantID, normScope.SiteID)
	}
	cloned, err := NewSiteTwin(stored)
	if err != nil {
		return SiteTwin{}, err
	}
	evalAt := at.UTC()
	if evalAt.IsZero() {
		evalAt = time.Now().UTC()
	}
	cloned.LoadedAt = evalAt
	return cloned, nil
}
