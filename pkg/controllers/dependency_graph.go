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
	clusterDependencyGraph.AddDependency(IndexNode, []MilvusComponent{})
	clusterDependencyGraph.AddDependency(RootCoord, []MilvusComponent{IndexNode})
	clusterDependencyGraph.AddDependency(DataCoord, []MilvusComponent{RootCoord})
	clusterDependencyGraph.AddDependency(IndexCoord, []MilvusComponent{DataCoord})
	clusterDependencyGraph.AddDependency(QueryCoord, []MilvusComponent{IndexCoord})
	clusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{QueryCoord})
	clusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{QueryNode})
	clusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{DataNode})

	mixCoordClusterDependencyGraph.AddDependency(IndexNode, []MilvusComponent{})
	mixCoordClusterDependencyGraph.AddDependency(MixCoord, []MilvusComponent{IndexNode})
	mixCoordClusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{MixCoord})
	mixCoordClusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{QueryNode})
	mixCoordClusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{DataNode})

	streamingNodeClusterDependencyGraph.AddDependency(MixCoord, []MilvusComponent{})
	streamingNodeClusterDependencyGraph.AddDependency(StreamingNode, []MilvusComponent{MixCoord})
	streamingNodeClusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{StreamingNode})
	streamingNodeClusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{QueryNode})
	streamingNodeClusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{DataNode})

	upgrade26ClusterDependencyGraph.AddDependency(MixCoord, []MilvusComponent{})
	upgrade26ClusterDependencyGraph.AddDependency(StreamingNode, []MilvusComponent{MixCoord})
	upgrade26ClusterDependencyGraph.AddDependency(QueryNode, []MilvusComponent{StreamingNode})
	upgrade26ClusterDependencyGraph.AddDependency(DataNode, []MilvusComponent{QueryNode})
	upgrade26ClusterDependencyGraph.AddDependency(Proxy, []MilvusComponent{DataNode})
}

var (
	clusterDependencyGraph, mixCoordClusterDependencyGraph = newDependencyGraphImpl(), newDependencyGraphImpl()
	streamingNodeClusterDependencyGraph                    = newDependencyGraphImpl()
	upgrade26ClusterDependencyGraph                        = newDependencyGraphImpl()
)
