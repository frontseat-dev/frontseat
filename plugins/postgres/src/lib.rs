//! Starts PostgreSQL as a service for a test: the image, how it is known
//! to be ready, where a test reaches it and what a test is told.
use frontseat_plugin::prelude::*;

/// The user the server runs as on IMAGE: Alpine's postgres.
const USER: &str = "70";

/// What the server writes, on every official image.
const WRITABLE: [&str; 2] = ["/var/lib/postgresql", "/var/run/postgresql"];

/// The image started where an entity names none: PostgreSQL 17 on
/// Alpine, the official image, by the digest of its index, so that each
/// worker's architecture has its own. An entity that wants another
/// writes `image` beside `as`.
const IMAGE: &str = "docker.io/library/postgres:17-alpine@sha256:3540c0362c809df084ffac0ae69fe794515f9de92692f02178bcdce0ef0302ac";

/// The name of the one port PostgreSQL serves, and the port it serves
/// on where an entity names none.
const PORT: &str = "sql";
const DEFAULT_PORT: i32 = 5432;

/// The role, its password and the database a test is given where an
/// entity's environment names none. They are of a database that lives
/// for one test, on a network that holds nothing else.
const DEFAULT: &str = "postgres";

pub(crate) struct PostgresPlugin;

impl Plugin for PostgresPlugin {
    fn describe(&self) -> Description {
        Description {
            name: "postgres".into(),
            services: vec!["postgres".into()],
            ..Default::default()
        }
    }

    /// Each thing an entity wrote is kept, whole; each it left is said
    /// here, to fit what was written: readiness asks the port, the role
    /// and the database that hold, and a test is told the same. What
    /// cannot be made to fit is refused by name, since it would be a
    /// server that never becomes ready.
    fn service(&self, _name: &str, written: &Service) -> Result<Service, Error> {
        let refused = |reason: &str, message: String, help: String| {
            Error::new(Code::InvalidArgument, reason, message).help(help)
        };
        let mut service = written.clone();
        if service.image.is_empty() {
            service.image = IMAGE.into();
            // A sandbox runs no service as root: the image's own server
            // user, which its entrypoint would give itself to, on the
            // image this plugin pins (Alpine's postgres, 70). An image
            // written instead is told its user by what is written.
            if service.user.is_empty() {
                service.user = USER.into();
            }
        }
        // What the server writes, on every official image: its data and
        // its socket's directory, and nothing else of the image.
        if service.writable.is_empty() {
            service.writable = WRITABLE.iter().map(|d| d.to_string()).collect();
        }
        if service.ports.is_empty() {
            service.ports.insert(PORT.into(), DEFAULT_PORT);
        }
        let Some(port) = service.ports.get(PORT).copied() else {
            return Err(refused(
                "unnamed-port",
                format!(
                    "the ports written name no port {PORT}, which is the one PostgreSQL serves"
                ),
                format!("Write ports: {{{PORT}: {DEFAULT_PORT}}}, or leave ports out."),
            ));
        };
        if service.environment.is_empty() {
            for name in ["POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"] {
                service.environment.insert(name.into(), DEFAULT.into());
            }
        }
        let said = |name: &str| {
            service
                .environment
                .get(name)
                .filter(|v| !v.is_empty())
                .cloned()
        };
        // What is written is kept whole, so nothing can be added to it;
        // and the image starts no server it was given no way to let a
        // client in: a password, or the one rule that asks for none. A
        // password kept in a file is not served: nothing places a file
        // beside a service yet.
        let trusting = said("POSTGRES_HOST_AUTH_METHOD").as_deref() == Some("trust");
        if said("POSTGRES_PASSWORD").is_none() && !trusting {
            return Err(refused(
                "no-password",
                "the environment written gives PostgreSQL no POSTGRES_PASSWORD, and it does not start without one".into(),
                "Write POSTGRES_PASSWORD in environment, or leave environment out.".into(),
            ));
        }
        // The port is said in one place, ports: the server and every
        // client read PGPORT too, and readiness would ask another.
        if service.environment.contains_key("PGPORT") {
            return Err(refused(
                "port-in-environment",
                "the environment written sets PGPORT, and the port PostgreSQL serves is said by ports"
                    .into(),
                format!("Write ports: {{{PORT}: …}}, and leave PGPORT out of environment."),
            ));
        }
        // What the image makes of its environment: a role named by
        // POSTGRES_USER, and a database of the role's name unless
        // POSTGRES_DB names another.
        let user = said("POSTGRES_USER").unwrap_or_else(|| DEFAULT.into());
        let database = said("POSTGRES_DB").unwrap_or_else(|| user.clone());
        // The server is told its port by its arguments, and the port is
        // said in one place, ports. Arguments that are written are kept
        // as they are, so nothing can be added to them: they may not say
        // a port, in any way the server reads one, and they stand only
        // beside the port it serves on where it is told none.
        if let Some(sets) = service.arguments.iter().find(|a| sets_port(a)) {
            return Err(refused(
                "port-in-arguments",
                format!("the arguments written tell the server its port ({sets}), and the port PostgreSQL serves is said by ports"),
                format!("Leave the port out of arguments, and write ports: {{{PORT}: …}} with no arguments where it is not {DEFAULT_PORT}."),
            ));
        }
        if port != DEFAULT_PORT {
            if !service.arguments.is_empty() {
                return Err(refused(
                    "port-beside-arguments",
                    format!("ports says {port} beside arguments, and the server is told its port by arguments this plugin writes"),
                    format!("Leave arguments out, or leave ports out and serve on {DEFAULT_PORT}."),
                ));
            }
            // The image takes what begins with a dash as the server's
            // own switches.
            service.arguments = vec!["-p".into(), port.to_string()];
        }
        if service.ready.is_empty() {
            // Asked over the network, as a test will ask: while the image
            // makes its database the server listens on its socket alone.
            service.ready.push(ServiceReady {
                check: Some(service_ready::Check::Command(ServiceCommandReady {
                    arguments: [
                        "pg_isready",
                        "-h",
                        "{host}",
                        "-p",
                        &format!("{{port.{PORT}}}"),
                        "-U",
                        &user,
                        "-d",
                        &database,
                    ]
                    .map(String::from)
                    .to_vec(),
                })),
            });
        }
        if service.exports.is_empty() {
            // The names every client of PostgreSQL reads; the password
            // where one was said, and none where the server asks none.
            for (name, value) in [
                ("PGHOST", Some("{host}".to_string())),
                ("PGPORT", Some(format!("{{port.{PORT}}}"))),
                ("PGUSER", Some(user)),
                ("PGPASSWORD", said("POSTGRES_PASSWORD")),
                ("PGDATABASE", Some(database)),
            ] {
                if let Some(value) = value {
                    service.exports.insert(name.into(), value);
                }
            }
        }
        if service.endpoints.is_empty() {
            // Where a test reaches it: the one port it serves, on the
            // host it is given.
            service
                .endpoints
                .insert(PORT.into(), format!("{{host}}:{{port.{PORT}}}"));
        }
        Ok(service)
    }
}

/// Whether an argument of the server tells it its port, in any way it
/// reads one: `-p 6543`, `-p6543`, `--port=6543`, `--port 6543`, and the
/// setting itself, `-c port=6543` or `-cport=6543`.
fn sets_port(argument: &str) -> bool {
    let setting = |text: &str| {
        text.trim_start()
            .strip_prefix("port")
            .is_some_and(|rest| rest.trim_start().starts_with('='))
    };
    argument.starts_with("-p")
        || argument.starts_with("--port")
        || setting(argument)
        || argument.strip_prefix("-c").is_some_and(setting)
        || argument.strip_prefix("--").is_some_and(setting)
}

frontseat_plugin::serve!(PostgresPlugin);

#[cfg(test)]
mod tests {
    use super::*;
    use frontseat_plugin::service_ready::Check;

    fn ready(service: &Service) -> Vec<String> {
        match &service.ready[0].check {
            Some(Check::Command(command)) => command.arguments.clone(),
            other => panic!("ready by {other:?}"),
        }
    }

    #[test]
    fn nothing_written_is_a_database_a_test_reaches() {
        let service = PostgresPlugin
            .service("postgres", &Service::default())
            .unwrap();
        assert_eq!(service.image, IMAGE);
        assert_eq!(service.ports, [("sql".to_string(), 5432)].into());
        // It runs as the image's own server user, never as root, and
        // writes its data and its socket's directory alone.
        assert_eq!(service.user, "70");
        assert_eq!(
            service.writable,
            ["/var/lib/postgresql", "/var/run/postgresql"]
        );
        assert!(service.arguments.is_empty());
        assert_eq!(
            ready(&service),
            [
                "pg_isready",
                "-h",
                "{host}",
                "-p",
                "{port.sql}",
                "-U",
                "postgres",
                "-d",
                "postgres"
            ]
        );
        // How long it is given is the host's to say.
        assert_eq!(service.timeout, None);
        assert_eq!(service.exports["PGHOST"], "{host}");
        assert_eq!(service.exports["PGPORT"], "{port.sql}");
        assert_eq!(service.exports["PGDATABASE"], "postgres");
        // A test reaches it at its one port.
        assert_eq!(
            service.endpoints,
            [("sql".to_string(), "{host}:{port.sql}".to_string())].into()
        );
    }

    #[test]
    fn what_is_written_is_kept_whole_and_the_rest_fits_it() {
        let written = Service {
            image: "docker.io/library/postgres:16-alpine@sha256:0000000000000000000000000000000000000000000000000000000000000000".into(),
            ports: [("sql".to_string(), 6543)].into(),
            environment: [
                ("POSTGRES_USER".to_string(), "app".to_string()),
                ("POSTGRES_PASSWORD".to_string(), "secret".to_string()),
            ]
            .into(),
            exports: [("DATABASE".to_string(), "{host}:{port.sql}".to_string())].into(),
            timeout: Some(Duration {
                seconds: 30,
                nanos: 0,
            }),
            ..Default::default()
        };
        let service = PostgresPlugin.service("postgres", &written).unwrap();
        assert_eq!(service.image, written.image);
        // An image written is told its user by what is written: this
        // plugin knows the user of the image it pins alone. What every
        // official image writes is said all the same.
        assert_eq!(service.user, "");
        assert_eq!(
            service.writable,
            ["/var/lib/postgresql", "/var/run/postgresql"]
        );
        assert_eq!(service.ports, written.ports);
        assert_eq!(service.environment, written.environment);
        assert_eq!(service.exports, written.exports);
        assert_eq!(service.timeout, written.timeout);
        // Where a test reaches it is said of the port written.
        assert_eq!(service.endpoints["sql"], "{host}:{port.sql}");
        // The server is told the port, and asked on it for the role's
        // own database.
        assert_eq!(service.arguments, ["-p", "6543"]);
        assert_eq!(
            ready(&service),
            [
                "pg_isready",
                "-h",
                "{host}",
                "-p",
                "{port.sql}",
                "-U",
                "app",
                "-d",
                "app"
            ]
        );
    }

    #[test]
    fn ports_that_name_no_sql_port_are_refused() {
        let written = Service {
            ports: [("http".to_string(), 80)].into(),
            ..Default::default()
        };
        let refused = PostgresPlugin.service("postgres", &written).unwrap_err();
        assert_eq!(refused.reason, "unnamed-port");
    }

    fn refusal(written: Service) -> String {
        PostgresPlugin
            .service("postgres", &written)
            .expect_err("what cannot become ready was started")
            .reason
    }

    fn arguments(written: &[&str]) -> Service {
        Service {
            arguments: written.iter().map(|a| a.to_string()).collect(),
            ..Default::default()
        }
    }

    fn environment(pairs: &[(&str, &str)]) -> Service {
        Service {
            environment: pairs
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            ..Default::default()
        }
    }

    // A server given no way to let a client in never starts: an environment with
    // no password, an empty one, or a rule other than the one that asks
    // none.
    #[test]
    fn an_env_that_gives_no_way_in_is_refused() {
        assert_eq!(refusal(environment(&[("TZ", "UTC")])), "no-password");
        assert_eq!(
            refusal(environment(&[("POSTGRES_PASSWORD", "")])),
            "no-password"
        );
        assert_eq!(
            refusal(environment(&[(
                "POSTGRES_HOST_AUTH_METHOD",
                "scram-sha-256"
            )])),
            "no-password"
        );
        assert_eq!(
            refusal(environment(&[("POSTGRES_PASSWORD_FILE", "/run/secret")])),
            "no-password"
        );
        // The control: the rule that asks none is a way in, and a test is
        // then told no password.
        let trusting = PostgresPlugin
            .service(
                "postgres",
                &environment(&[("POSTGRES_HOST_AUTH_METHOD", "trust")]),
            )
            .unwrap();
        assert!(!trusting.exports.contains_key("PGPASSWORD"));
        assert_eq!(trusting.exports["PGUSER"], "postgres");
    }

    // The port is said by ports and nowhere else: not by the environment every
    // client and the server read, and not by the server's arguments, in
    // any spelling it takes.
    #[test]
    fn a_port_said_anywhere_but_ports_is_refused() {
        assert_eq!(
            refusal(environment(&[
                ("POSTGRES_PASSWORD", "x"),
                ("PGPORT", "6543")
            ])),
            "port-in-environment"
        );
        for spelling in [
            &["-p", "6543"][..],
            &["-p6543"],
            &["--port=6543"],
            &["--port", "6543"],
            &["-c", "port=6543"],
            &["-c", "port = 6543"],
            &["-cport=6543"],
            &["-p", "{port.sql}"],
        ] {
            assert_eq!(
                refusal(arguments(spelling)),
                "port-in-arguments",
                "{spelling:?}"
            );
        }
        // The control: arguments that say no port are kept, on the port
        // the server serves on where it is told none.
        let kept = PostgresPlugin
            .service(
                "postgres",
                &arguments(&["-c", "fsync=off", "-c", "max_connections=20"]),
            )
            .unwrap();
        assert_eq!(
            kept.arguments,
            ["-c", "fsync=off", "-c", "max_connections=20"]
        );
        assert_eq!(kept.ports["sql"], 5432);
    }

    // Arguments are kept whole, so beside another port the server could
    // not be told it: refused, with help that is itself accepted.
    #[test]
    fn args_beside_another_port_are_refused_and_either_alone_is_started() {
        let both = Service {
            ports: [("sql".to_string(), 6543)].into(),
            ..arguments(&["-c", "fsync=off"])
        };
        assert_eq!(refusal(both), "port-beside-arguments");
        let port_alone = Service {
            ports: [("sql".to_string(), 6543)].into(),
            ..Default::default()
        };
        assert_eq!(
            PostgresPlugin
                .service("postgres", &port_alone)
                .unwrap()
                .arguments,
            ["-p", "6543"]
        );
        assert!(PostgresPlugin
            .service("postgres", &arguments(&["-c", "fsync=off"]))
            .is_ok());
    }

    // Endpoints written are kept whole, as every field written is: none
    // of the plugin's own is added beside them.
    #[test]
    fn endpoints_written_are_kept_whole() {
        let written = Service {
            endpoints: [(
                "db".to_string(),
                "postgres://{host}:{port.sql}/app".to_string(),
            )]
            .into(),
            ..Default::default()
        };
        let service = PostgresPlugin.service("postgres", &written).unwrap();
        assert_eq!(service.endpoints, written.endpoints);
    }

    // Asked as the host asks it, through the module's dispatch: the
    // answer is the whole service, endpoints with it.
    #[test]
    fn asked_as_the_host_asks_it_the_answer_is_whole() {
        let plugin = frontseat_plugin_test::TestPlugin::new(crate::served());
        assert_eq!(plugin.describe().services, ["postgres"]);
        let answered = plugin
            .service("postgres", Service::default())
            .answered()
            .service
            .expect("the whole service");
        assert_eq!(
            answered,
            PostgresPlugin
                .service("postgres", &Service::default())
                .unwrap()
        );
        assert_eq!(answered.endpoints["sql"], "{host}:{port.sql}");
        plugin
            .service(
                "postgres",
                Service {
                    ports: [("http".to_string(), 80)].into(),
                    ..Default::default()
                },
            )
            .refused("unnamed-port");
    }
}

#[cfg(test)]
frontseat_plugin_test::readme_says_the_options!();
