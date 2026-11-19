//! Constructors for the actions every ecosystem plugin declares alike.
use crate::{Action, Output};

impl Action {
    /// A fetch: an action that brings what a lock names from outside,
    /// shared by every project of the repository that locks it alike, and
    /// leaving each of `outputs`, which it must. The plugin fills in how
    /// it fetches (its downloads, its command or the files it is told):
    ///
    /// ```ignore
    /// Action { downloads, ..Action::fetch("fetch/itoa@1.0.18", ["~/.cargo/vendor/itoa-1.0.18/"]) }
    /// ```
    pub fn fetch<O: Into<String>>(
        name: impl Into<String>,
        outputs: impl IntoIterator<Item = O>,
    ) -> Self {
        Self {
            name: name.into(),
            outputs: outputs
                .into_iter()
                .map(|path| Output {
                    path: path.into(),
                    optional: false,
                })
                .collect(),
            fetch: true,
            shared: true,
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::{Action, Output};

    #[test]
    fn a_fetch_is_shared_and_leaves_what_it_must() {
        let fetch = Action::fetch("fetch/a", ["~/a/", "~/b"]);
        assert_eq!(
            fetch,
            Action {
                name: "fetch/a".into(),
                outputs: vec![
                    Output {
                        path: "~/a/".into(),
                        optional: false
                    },
                    Output {
                        path: "~/b".into(),
                        optional: false
                    },
                ],
                fetch: true,
                shared: true,
                ..Default::default()
            }
        );
    }
}
