//! Starts a container as it is written: the plugin for whatever service
//! no other plugin knows how to start.
use frontseat_plugin::prelude::*;

pub(crate) struct ContainerPlugin;

impl Plugin for ContainerPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "container".into(),
            services: vec!["container".into()],
            ..Default::default()
        }
    }

    /// A container is started as its entity wrote it, and with nothing
    /// more: this plugin knows nothing of what runs in it. What a service
    /// must say, an image, a port and when it is ready, the host holds
    /// every service to.
    fn service(&self, _name: &str, written: &Service) -> Result<Service, Error> {
        Ok(written.clone())
    }
}

frontseat_plugin::serve!(ContainerPlugin);

#[cfg(test)]
mod tests {
    use super::*;
    use frontseat_plugin_test::TestPlugin;

    fn started(written: Service) -> Service {
        TestPlugin::new(crate::served())
            .service("container", written)
            .answered()
            .service
            .expect("the whole service")
    }

    #[test]
    fn what_is_written_is_what_is_started() {
        let written = Service {
            image: "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec".into(),
            arguments: vec!["nc".into(), "-lk".into()],
            ports: [("greet".to_string(), 7000)].into(),
            endpoints: [("greet".to_string(), "{host}:{port.greet}".to_string())].into(),
            ..Default::default()
        };
        assert_eq!(started(written.clone()), written);
    }

    #[test]
    fn nothing_written_is_nothing_added() {
        assert_eq!(started(Service::default()), Service::default());
    }

    #[test]
    fn it_starts_the_container_service_and_no_other() {
        let plugin = TestPlugin::new(crate::served());
        assert_eq!(plugin.describe().services, ["container"]);
        plugin
            .service("postgres", Service::default())
            .refused("unclaimed-service");
    }
}

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
