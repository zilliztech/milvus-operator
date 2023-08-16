package controllers

type dependencyGraph interface {
	AddDependency(component MilvusComponent, dependencies []MilvusComponent)
	GetDependencies(component MilvusComponent) []MilvusComponent
	// GetReversedDependencies returns the reversed dependencies of the component
	// it's used for downgrade
	GetReversedDependencies(component MilvusComponent) []MilvusComponent
}

type dependencyGraphImpl struct {
	dependencies         map[MilvusComponent][]MilvusComponent
	reversedDependencies map[MilvusComponent][]MilvusComponent
}

func newDependencyGraphImpl() dependencyGraph {
	return &dependencyGraphImpl{
		dependencies:         make(map[MilvusComponent][]MilvusComponent),
		reversedDependencies: make(map[MilvusComponent][]MilvusComponent),
	}
}

func (d *dependencyGraphImpl) AddDependency(component MilvusComponent, dependencies []MilvusComponent) {
	d.dependencies[component] = dependencies
	for _, dep := range dependencies {
		if d.reversedDependencies[dep] == nil {
			d.reversedDependencies[dep] = []MilvusComponent{}
		}
		d.reversedDependencies[dep] = append(d.reversedDependencies[dep], component)
	}
}

func (d *dependencyGraphImpl) GetDependencies(component MilvusComponent) []MilvusComponent {
	return d.dependencies[component]
}

func (d *dependencyGraphImpl) GetReversedDependencies(component MilvusComponent) []MilvusComponent {
	return d.reversedDependencies[component]
}

func init() {
	// v2.2.x
	clusterDependencyGraph.AddDependency(RootCoord, []MilvusComponent{})
	clusterDependencyGraph.AddDependency(DataCoord, []MilvusComponent{RootCoord})
	clusterDependencyGraph.AddDependency(IndexCoord, []MilvusComponent{DataCoord})
	clusterDependencyGraph.AddDependency(QueryCoord, []MilvusComponent{IndexCoord})
	clusterDependencyGraph.AddDependency(IndexNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{IndexNode, QueryNode, DataNode})

	// v2.3+
	clusterDependencyGraphV2p3.AddDependency(RootCoord, []MilvusComponent{})
	clusterDependencyGraphV2p3.AddDependency(IndexCoord, []MilvusComponent{RootCoord})
	clusterDependencyGraphV2p3.AddDependency(DataCoord, []MilvusComponent{IndexCoord})
	clusterDependencyGraphV2p3.AddDependency(QueryCoord, []MilvusComponent{DataCoord})
	clusterDependencyGraphV2p3.AddDependency(IndexNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraphV2p3.AddDependency(QueryNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraphV2p3.AddDependency(DataNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraphV2p3.AddDependency(Proxy, []MilvusComponent{IndexNode, QueryNode, DataNode})

	mixCoordClusterDependencyGraph.AddDependency(MixCoord, []MilvusComponent{})
	mixCoordClusterDependencyGraph.AddDependency(IndexNode, []MilvusComponent{MixCoord})
	mixCoordClusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{MixCoord})
	mixCoordClusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{MixCoord})
	mixCoordClusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{IndexNode, QueryNode, DataNode})
}

var (
	clusterDependencyGraph, mixCoordClusterDependencyGraph = newDependencyGraphImpl(), newDependencyGraphImpl()
	clusterDependencyGraphV2p3                             = newDependencyGraphImpl()
)
