```text
                  Event received      +----------------------+
                  ------------------->|      Reconcile       |
                                      +----------+-----------+
                                                 |
                                                 |          True
                                              Deleted? ------------>Exit
                                                 |
                                                 |
                                                 v
                                     Start dependencies reconcile
                    +----------------------------+----------------------------+
                    |                            |                            |
                    |                            |                            |
                    |                            |                            |
        +-----------+-----------+    +-----------+-----------+    +-----------+---------+
        |     Etcd Reconcile    |    |    Pulsar Reconcile   |    |   MinIO Reconcile   |
        +-----------+-----------+    +-----------+-----------+    +-----------+---------+
                    |                            |                            |
                    |                            |                            |
                    |                            |                            |
                    |                            |                            |
                    v                            v                            v
              Apply or update              Apply or update              Apply or update
                    |                            |                            |
                    |                            |                            |
                    |                            |                            |
                    |                            |                            |
                    |                            v                            |
                    +------------------> Any error occurred? <----------------+
                                                 |      |
                                                 | NO   |             YES
                                                 |      +----------------------------> Exit
                                                 |                                      |
                                                 v                   NO                 |
                                    All dependencies are ready? ---------------> Exit   |
                                                 |                                |     |
                                                 | YES                            |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 v                                |     |
                                      +----------------------+                    |     |
                                      |   Milvus Reconcile   |                    |     |
                                      +----------+-----------+                    |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 v                                |     |
                                          Apply or update                         |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 |                                |     |
                                                 v              YES               |     |
                                        Any error occurred? -----------> Exit     |     |
                                                 |                        |       |     |
                                                 |                        |       |     |
                                                 | NO                     |       |     |
                                                 |                        |       |     |
                                                 v                        |       |     |
                                             Completed                    |       |     |
                                                 |                        |       |     |
                                                 |                        |       |     |
                                                 |                        |       |     |
                                                 |                        |       |     |
                                  +--------------+--------------+         |       |     |
                                  |  Set status & conditions    | <-------+-------+-----+
                                  +-----------------------------+
```